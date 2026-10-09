// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cijoboutcomes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// JobURI identifies a run's artifact root, without a trailing slash.
type JobURI string

var _ utils.LoggableKey = JobURI("")

func (uri JobURI) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("jobUri", string(uri))
}

var gcsBucketComponent = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*[a-z0-9]$|^[a-z0-9]$`)
var prowPathComponent = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var decimalID = regexp.MustCompile(`^[1-9][0-9]*$`)

func validateGCSBucket(bucket string) error {
	if len(bucket) < 3 || len(bucket) > 222 {
		return fmt.Errorf("invalid GCS bucket %q", bucket)
	}
	for _, component := range strings.Split(bucket, ".") {
		if len(component) > 63 || !gcsBucketComponent.MatchString(component) {
			return fmt.Errorf("invalid GCS bucket %q", bucket)
		}
	}
	return nil
}

// CanonicalJobURI accepts a gs:// URI for an exact Prow run root. Any structurally
// valid bucket is allowed; GCSClient enforces its configured bucket separately.
// Escapes, credentials, queries, fragments, traversal, and artifact suffixes are
// rejected rather than normalized. One optional trailing slash is removed.
func CanonicalJobURI(raw string) (JobURI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid job URI %q: %w", raw, err)
	}
	if u.Scheme != "gs" || u.User != nil || u.Opaque != "" || strings.ContainsAny(raw, "%?#\\") {
		return "", fmt.Errorf("invalid job URI %q", raw)
	}
	if err := validateGCSBucket(u.Host); err != nil {
		return "", err
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	parts := strings.Split(prefix, "/")
	for _, part := range parts {
		if !prowPathComponent.MatchString(part) || part == "." || part == ".." {
			return "", fmt.Errorf("invalid job URI path %q", u.Path)
		}
	}
	info, err := snapshot.ParseProwURL("https://prow.ci.openshift.org/view/gs/" + u.Host + "/" + prefix)
	if err != nil {
		return "", err
	}
	if info.GCSPrefix != prefix {
		return "", fmt.Errorf("job URI must identify a run root: %q", raw)
	}
	if parts[0] == "pr-logs" && parts[2] != "batch" && !decimalID.MatchString(parts[3]) {
		return "", fmt.Errorf("invalid pull request number in %q", raw)
	}
	if _, err := BuildIDTime(info.ProwID); err != nil {
		return "", err
	}
	return JobURI("gs://" + u.Host + "/" + prefix), nil
}

// Info uses the shared snapshot parser, including its Prow presentation URL.
func (uri JobURI) Info() (*snapshot.ProwJobInfo, error) {
	canonical, err := CanonicalJobURI(string(uri))
	if err != nil {
		return nil, err
	}
	return snapshot.ParseProwURL("https://prow.ci.openshift.org/view/gs/" + strings.TrimPrefix(string(canonical), "gs://"))
}

type DiscoveredJob struct {
	JobURI  JobURI `json:"jobUri"`
	BuildID string `json:"buildId"`
	JobName string `json:"jobName"`
}

func JobReference(uri JobURI) (DiscoveredJob, error) {
	info, err := uri.Info()
	if err != nil {
		return DiscoveredJob{}, err
	}
	return DiscoveredJob{
		JobURI:  JobURI("gs://" + info.GCSBucket + "/" + info.GCSPrefix),
		BuildID: info.ProwID, JobName: info.JobName,
	}, nil
}

const (
	snowflakeEpochMS int64 = 1288834974657
	snowflakeShift         = 22
	minBuildID       int64 = 1000000000000000000
	maxSnowflakeMS         = snowflakeEpochMS + (math.MaxInt64 >> snowflakeShift)
)

// BuildIDTime decodes contemporary Prow snowflakes with integer arithmetic.
// Only canonical 19-digit positive signed-int64 IDs are supported: their decimal
// ordering is chronological, unlike mixed-width IDs. Public 2026 runs fall in
// this range. Shorter historical IDs are errors, not silently skipped runs.
func BuildIDTime(id string) (time.Time, error) {
	if len(id) != 19 || !decimalID.MatchString(id) {
		return time.Time{}, fmt.Errorf("build ID %q must be a 19-digit snowflake", id)
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid build ID %q: %w", id, err)
	}
	return time.UnixMilli((n >> snowflakeShift) + snowflakeEpochMS).UTC(), nil
}

// SnowflakeLowerBound returns the first supported ID whose timestamp is >= t.
// It rounds fractional milliseconds up, clamps times before the 19-digit range
// to 1000000000000000000, and errors beyond the signed-int64 snowflake range.
func SnowflakeLowerBound(t time.Time) (string, error) {
	if t.After(time.UnixMilli(maxSnowflakeMS)) {
		return "", fmt.Errorf("time %s exceeds the snowflake range", t)
	}
	minimum := time.UnixMilli(snowflakeEpochMS + (minBuildID >> snowflakeShift))
	if !t.After(minimum) {
		return strconv.FormatInt(minBuildID, 10), nil
	}
	ms := t.UnixMilli()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		ms++
	}
	return strconv.FormatInt((ms-snowflakeEpochMS)<<snowflakeShift, 10), nil
}

// GCSJob is one independently cursored listing scope. Prefix is bucket-relative
// and ends in '/'. Alias scopes resolve to PR run URIs, not directory URIs;
// controllers must filter their cursors by job and canonical run path family.
type GCSJob struct {
	Name, Prefix string
	Aliases      bool
}

type GCSClient struct {
	Client    *http.Client
	Bucket    string
	APIBase   string // Defaults to https://storage.googleapis.com/storage/v1.
	JobFilter string // Case-insensitive substring of the job name.
}

var gcsJobRoots = []GCSJob{
	{Name: "pull-ci-Azure-ARO-HCP-", Prefix: "pr-logs/directory/", Aliases: true},
	{Name: "pull-ci-Azure-ARO-HCP-", Prefix: "pr-logs/pull/batch/"},
	{Name: "branch-ci-Azure-ARO-HCP-", Prefix: "logs/"},
	{Name: "periodic-ci-Azure-ARO-HCP-", Prefix: "logs/"},
}

type gcsObject struct {
	Name     string            `json:"name"`
	Bucket   string            `json:"bucket"`
	Metadata map[string]string `json:"metadata"`
}

type gcsListPage struct {
	Prefixes      []string    `json:"prefixes"`
	Items         []gcsObject `json:"items"`
	NextPageToken string      `json:"nextPageToken"`
}

// ListJobs discovers dynamic job names without descending into their runs.
func (c *GCSClient) ListJobs(ctx context.Context) ([]GCSJob, error) {
	var jobs []GCSJob
	for _, root := range gcsJobRoots {
		err := c.listPages(ctx, root.Prefix+root.Name, "", "", func(page gcsListPage) error {
			if len(page.Items) != 0 {
				return fmt.Errorf("unexpected objects at job-name scope %q", root.Prefix+root.Name)
			}
			for _, prefix := range page.Prefixes {
				name := strings.TrimSuffix(strings.TrimPrefix(prefix, root.Prefix), "/")
				if prefix != root.Prefix+name+"/" || !strings.HasPrefix(name, root.Name) || !prowPathComponent.MatchString(name) {
					return fmt.Errorf("unexpected job prefix %q under %q", prefix, root.Prefix+root.Name)
				}
				if strings.Contains(strings.ToLower(name), strings.ToLower(c.JobFilter)) {
					jobs = append(jobs, GCSJob{Name: name, Prefix: prefix, Aliases: root.Aliases})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// ListRuns emits runs in the inclusive [since, until] timestamp window. A zero
// since lists all supported history; until must be an explicit fixed instant.
// Every listing uses delimiter '/', never recursively enumerating artifacts.
// The caller owns canonical deduplication, persistence, and per-scope cursors;
// a partial traversal or emit error must not advance its cursor.
func (c *GCSClient) ListRuns(ctx context.Context, job GCSJob, since, until time.Time, emit func(JobURI) error) error {
	valid := false
	for _, root := range gcsJobRoots {
		if strings.HasPrefix(job.Name, root.Name) && prowPathComponent.MatchString(job.Name) && job.Prefix == root.Prefix+job.Name+"/" && job.Aliases == root.Aliases {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid GCS job scope: %+v", job)
	}
	if until.IsZero() || (!since.IsZero() && since.After(until)) || emit == nil {
		return fmt.Errorf("invalid run discovery window or callback")
	}
	if until.After(time.UnixMilli(maxSnowflakeMS)) {
		return fmt.Errorf("until %s exceeds the snowflake range", until)
	}
	minimum := time.UnixMilli(snowflakeEpochMS + (minBuildID >> snowflakeShift))
	if until.Before(minimum) {
		return nil
	}
	start := ""
	if !since.IsZero() {
		lower, err := SnowflakeLowerBound(since)
		if err != nil {
			return err
		}
		start = job.Prefix + lower
	}
	// GCS endOffset is exclusive. Include every sequence/node bit at until's
	// millisecond; uint64 also represents the sentinel just beyond MaxInt64.
	upper := uint64(until.UnixMilli()-snowflakeEpochMS+1) << snowflakeShift
	end := job.Prefix + strconv.FormatUint(upper, 10)
	return c.listPages(ctx, job.Prefix, start, end, func(page gcsListPage) error {
		emitRun := func(id, target string) error {
			stamp, err := BuildIDTime(id)
			if err != nil {
				return err
			}
			uri, err := CanonicalJobURI(target)
			if err != nil {
				return err
			}
			info, err := uri.Info()
			if err != nil {
				return err
			}
			if info.GCSBucket != c.Bucket || info.JobName != job.Name || info.ProwID != id {
				return fmt.Errorf("run target %q does not match bucket %q, job %q, build %q", uri, c.Bucket, job.Name, id)
			}
			if job.Aliases && !strings.HasPrefix(info.GCSPrefix, "pr-logs/pull/Azure_ARO-HCP/") {
				return fmt.Errorf("alias target %q is not an Azure/ARO-HCP PR run", uri)
			}
			if (!since.IsZero() && stamp.Before(since)) || stamp.After(until) {
				return nil
			}
			return emit(uri)
		}
		for _, prefix := range page.Prefixes {
			if job.Aliases || !strings.HasPrefix(prefix, job.Prefix) || !strings.HasSuffix(prefix, "/") {
				return fmt.Errorf("unexpected run prefix %q under %q", prefix, job.Prefix)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(prefix, job.Prefix), "/")
			if err := emitRun(id, "gs://"+c.Bucket+"/"+prefix); err != nil {
				return err
			}
		}
		for _, object := range page.Items {
			if !strings.HasPrefix(object.Name, job.Prefix) || (object.Bucket != "" && object.Bucket != c.Bucket) {
				return fmt.Errorf("unexpected object %q in bucket %q", object.Name, object.Bucket)
			}
			name := strings.TrimPrefix(object.Name, job.Prefix)
			if name == "latest-build.txt" {
				continue
			}
			if !job.Aliases || !strings.HasSuffix(name, ".txt") {
				return fmt.Errorf("unexpected run object %q", object.Name)
			}
			id := strings.TrimSuffix(name, ".txt")
			if _, err := BuildIDTime(id); err != nil {
				return err
			}
			target, present := object.Metadata["x-goog-meta-link"]
			if !present {
				response, err := c.get(ctx, object.Name, url.Values{"alt": {"media"}})
				if err != nil {
					return err
				}
				body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
				_ = response.Body.Close()
				if err != nil {
					return fmt.Errorf("read alias %q: %w", object.Name, err)
				}
				if len(body) > 4096 {
					return fmt.Errorf("alias %q exceeds 4096 bytes", object.Name)
				}
				target = strings.TrimSpace(string(body))
			}
			if err := emitRun(id, target); err != nil {
				return fmt.Errorf("alias %q: %w", object.Name, err)
			}
		}
		return nil
	})
}

func (c *GCSClient) listPages(ctx context.Context, prefix, start, end string, visit func(gcsListPage) error) error {
	query := url.Values{
		"prefix": {prefix}, "delimiter": {"/"}, "maxResults": {"1000"},
		"fields": {"nextPageToken,prefixes,items(name,bucket,metadata)"},
	}
	if start != "" {
		query.Set("startOffset", start)
	}
	if end != "" {
		query.Set("endOffset", end)
	}
	seen := map[string]bool{}
	for {
		response, err := c.get(ctx, "", query)
		if err != nil {
			return err
		}
		var page gcsListPage
		err = json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if err != nil {
			return fmt.Errorf("decode GCS listing for %q: %w", prefix, err)
		}
		if err := visit(page); err != nil {
			return err
		}
		if page.NextPageToken == "" {
			return nil
		}
		if seen[page.NextPageToken] {
			return fmt.Errorf("repeated GCS page token for %q", prefix)
		}
		seen[page.NextPageToken] = true
		query.Set("pageToken", page.NextPageToken)
	}
}

func (c *GCSClient) get(ctx context.Context, object string, query url.Values) (*http.Response, error) {
	if err := validateGCSBucket(c.Bucket); err != nil {
		return nil, err
	}
	base := c.APIBase
	if base == "" {
		base = "https://storage.googleapis.com/storage/v1"
	}
	endpoint := strings.TrimSuffix(base, "/") + "/b/" + url.PathEscape(c.Bucket) + "/o"
	if object != "" {
		endpoint += "/" + url.PathEscape(object)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GCS GET: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("GCS GET %s: %s", request.URL, response.Status)
	}
	return response, nil
}
