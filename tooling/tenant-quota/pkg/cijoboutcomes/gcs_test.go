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
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

const (
	gcsTestBucket = "test-platform-results-public"
	gcsTestJob    = "pull-ci-Azure-ARO-HCP-main-unit"
	gcsTestID     = "2104421180684374016"
)

func TestCanonicalJobURI(t *testing.T) {
	for _, prefix := range []string{
		"logs/branch-ci-Azure-ARO-HCP-main-unit/",
		"logs/periodic-ci-Azure-ARO-HCP-main-e2e/",
		"pr-logs/pull/Azure_ARO-HCP/7178/" + gcsTestJob + "/",
		"pr-logs/pull/batch/" + gcsTestJob + "/",
	} {
		t.Run(prefix, func(t *testing.T) {
			raw := "gs://" + gcsTestBucket + "/" + prefix + gcsTestID
			uri, err := CanonicalJobURI(raw + "/")
			require.NoError(t, err)
			require.Equal(t, JobURI(raw), uri)
			info, err := uri.Info()
			require.NoError(t, err)
			require.Equal(t, gcsTestBucket, info.GCSBucket)
			require.Equal(t, prefix+gcsTestID, info.GCSPrefix)
			require.Equal(t, gcsTestID, info.ProwID)
			require.Equal(t, "https://prow.ci.openshift.org/view/gs/"+gcsTestBucket+"/"+prefix+gcsTestID, info.URL)
			row, err := JobReference(JobURI(raw + "/"))
			require.NoError(t, err)
			require.Equal(t, DiscoveredJob{JobURI: uri, BuildID: gcsTestID, JobName: info.JobName}, row)
			data, err := json.Marshal(row)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`{"jobUri":%q,"buildId":%q,"jobName":%q}`, raw, gcsTestID, info.JobName), string(data))
			uri.AddLoggerValues(logr.Discard())
		})
	}
	_, err := CanonicalJobURI("gs://custom.config-bucket/logs/job/" + gcsTestID)
	require.NoError(t, err, "bucket selection belongs to the client, not URI parsing")

	base := "gs://" + gcsTestBucket + "/logs/job/" + gcsTestID
	for _, raw := range []string{
		"", "https://prow.ci.openshift.org/view/gs/" + gcsTestBucket + "/logs/job/" + gcsTestID,
		"gs://user@" + gcsTestBucket + "/logs/job/" + gcsTestID,
		"gs://" + gcsTestBucket + ":443/logs/job/" + gcsTestID,
		"gs:///logs/job/" + gcsTestID, "gs://BAD/logs/job/" + gcsTestID,
		"gs://a..b/logs/job/" + gcsTestID, "gs://ab/logs/job/" + gcsTestID,
		base + "?", base + "?key=value", base + "#", base + "#fragment", base + "//",
		base + "/artifacts", base + "/../" + gcsTestID,
		strings.Replace(base, "/job/", "/../", 1), strings.Replace(base, "/job/", "/./", 1),
		strings.Replace(base, "/job/", "//job/", 1), strings.Replace(base, "job", "job%2Fother", 1),
		strings.Replace(base, "job", "%2e%2e", 1), strings.Replace(base, "job", "job%252fother", 1),
		strings.Replace(base, "job", "job\\other", 1), strings.Replace(base, "job", "job name", 1),
		strings.Replace(base, gcsTestID, "123", 1), strings.Replace(base, gcsTestID, "9223372036854775808", 1),
		"gs://" + gcsTestBucket + "/pr-logs/directory/" + gcsTestJob + "/" + gcsTestID + ".txt",
		"gs://" + gcsTestBucket + "/pr-logs/pull/Azure_ARO-HCP/no-pr/" + gcsTestJob + "/" + gcsTestID,
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := CanonicalJobURI(raw)
			require.Error(t, err)
			_, err = JobURI(raw).Info()
			require.Error(t, err)
			_, err = JobReference(JobURI(raw))
			require.Error(t, err)
		})
	}
}

func TestSnowflakeBounds(t *testing.T) {
	// First main-api-validation alias returned by the public bucket's ordered
	// listing on 2026-10-09. This fixture needs no network access.
	publicTime, err := BuildIDTime("2100161384141557760")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 16, 9, 54, 27, 748000000, time.UTC), publicTime)
	for _, id := range []string{"1000000000000000000", gcsTestID, "9223372036854775807"} {
		stamp, err := BuildIDTime(id)
		require.NoError(t, err)
		require.Equal(t, time.UTC, stamp.Location())
		lower, err := SnowflakeLowerBound(stamp)
		require.NoError(t, err)
		require.LessOrEqual(t, lower, id)
		lowerTime, err := BuildIDTime(lower)
		require.NoError(t, err)
		require.Equal(t, stamp, lowerTime)
	}
	for _, id := range []string{"", "0", "123", "999999999999999999", "0100000000000000000", "+1000000000000000000", "-1000000000000000000", "100000000000000000x", "9223372036854775808", "18446744073709551615"} {
		_, err := BuildIDTime(id)
		require.Error(t, err, id)
	}
	for _, stamp := range []time.Time{{}, time.UnixMilli(snowflakeEpochMS), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)} {
		lower, err := SnowflakeLowerBound(stamp)
		require.NoError(t, err)
		require.Equal(t, "1000000000000000000", lower)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lower, err := SnowflakeLowerBound(stamp)
	require.NoError(t, err)
	require.Equal(t, "2006515713438646272", lower)
	decoded, err := BuildIDTime(lower)
	require.NoError(t, err)
	require.Equal(t, stamp, decoded)
	ceil, err := SnowflakeLowerBound(stamp.Add(time.Nanosecond))
	require.NoError(t, err)
	n, err := strconv.ParseInt(lower, 10, 64)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(n+(1<<22), 10), ceil)
	local, err := SnowflakeLowerBound(stamp.In(time.FixedZone("offset", 3600)))
	require.NoError(t, err)
	require.Equal(t, lower, local)
	minimum, err := BuildIDTime("1000000000000000000")
	require.NoError(t, err)
	ceil, err = SnowflakeLowerBound(minimum.Add(time.Nanosecond))
	require.NoError(t, err)
	require.Equal(t, "1000000000001835008", ceil)
	for _, stamp := range []time.Time{time.UnixMilli(maxSnowflakeMS).Add(time.Nanosecond), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), time.Unix(math.MaxInt64/2, 0)} {
		_, err := SnowflakeLowerBound(stamp)
		require.Error(t, err)
	}
}

func newGCSFixture(t *testing.T, handler http.HandlerFunc) *GCSClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &GCSClient{Client: server.Client(), Bucket: gcsTestBucket, APIBase: server.URL + "/storage/v1"}
}

func TestGCSListJobs(t *testing.T) {
	requests := 0
	client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/storage/v1/b/"+gcsTestBucket+"/o", r.URL.Path)
		q := r.URL.Query()
		require.Equal(t, "/", q.Get("delimiter"))
		require.Empty(t, q.Get("startOffset"))
		require.Empty(t, q.Get("endOffset"))
		var root *GCSJob
		for _, candidate := range gcsJobRoots {
			if q.Get("prefix") == candidate.Prefix+candidate.Name {
				root = &candidate
			}
		}
		require.NotNil(t, root, "must only list the four job name scopes")
		page := gcsListPage{}
		switch q.Get("pageToken") {
		case "":
			page.NextPageToken = "empty"
		case "empty":
			page.NextPageToken = "last"
		case "last":
			page.Prefixes = []string{root.Prefix + root.Name + "new-UNIT/", root.Prefix + root.Name + "e2e/"}
		default:
			t.Errorf("unexpected page token: %q", q.Get("pageToken"))
		}
		require.NoError(t, json.NewEncoder(w).Encode(page))
	})
	client.JobFilter = "uNiT"
	jobs, err := client.ListJobs(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 4)
	require.Equal(t, 12, requests)
	for i, root := range gcsJobRoots {
		require.Equal(t, GCSJob{Name: root.Name + "new-UNIT", Prefix: root.Prefix + root.Name + "new-UNIT/", Aliases: root.Aliases}, jobs[i])
	}
}

func TestGCSListRunsBoundsAndPagination(t *testing.T) {
	for _, root := range gcsJobRoots[1:] {
		t.Run(root.Prefix+root.Name, func(t *testing.T) {
			job := GCSJob{Name: root.Name + "main-unit", Prefix: root.Prefix + root.Name + "main-unit/"}
			since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			until := since.Add(time.Millisecond)
			lower, err := SnowflakeLowerBound(since)
			require.NoError(t, err)
			n, err := strconv.ParseInt(lower, 10, 64)
			require.NoError(t, err)
			ids := []string{strconv.FormatInt(n-1, 10), lower, strconv.FormatInt(n+(1<<22)-1, 10), strconv.FormatInt(n+(1<<23)-1, 10), strconv.FormatInt(n+(1<<23), 10)}
			requests := 0
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				q := r.URL.Query()
				require.Equal(t, job.Prefix, q.Get("prefix"), "must not list beneath a run prefix")
				require.Equal(t, "/", q.Get("delimiter"))
				require.Equal(t, job.Prefix+lower, q.Get("startOffset"))
				require.Equal(t, job.Prefix+ids[4], q.Get("endOffset"))
				page := gcsListPage{}
				switch q.Get("pageToken") {
				case "":
					page.NextPageToken = "second"
				case "second":
					for _, id := range ids {
						page.Prefixes = append(page.Prefixes, job.Prefix+id+"/")
					}
					page.NextPageToken = "third"
				case "third":
					page.Items = []gcsObject{{Name: job.Prefix + "latest-build.txt"}}
				default:
					t.Errorf("unexpected token %q", q.Get("pageToken"))
				}
				require.NoError(t, json.NewEncoder(w).Encode(page))
			})
			var got []JobURI
			err = client.ListRuns(t.Context(), job, since, until, func(uri JobURI) error { got = append(got, uri); return nil })
			require.NoError(t, err)
			require.Equal(t, 3, requests)
			require.Equal(t, []JobURI{
				JobURI("gs://" + gcsTestBucket + "/" + job.Prefix + ids[1]),
				JobURI("gs://" + gcsTestBucket + "/" + job.Prefix + ids[2]),
				JobURI("gs://" + gcsTestBucket + "/" + job.Prefix + ids[3]),
			}, got)
		})
	}
}

func TestGCSListRunsAliases(t *testing.T) {
	job := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/directory/" + gcsTestJob + "/", Aliases: true}
	target := "gs://" + gcsTestBucket + "/pr-logs/pull/Azure_ARO-HCP/7178/" + gcsTestJob + "/" + gcsTestID
	for _, metadata := range []bool{true, false} {
		t.Run(fmt.Sprint(metadata), func(t *testing.T) {
			downloads := 0
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("alt") == "media" {
					downloads++
					require.Equal(t, "/storage/v1/b/"+gcsTestBucket+"/o/"+url.PathEscape(job.Prefix+gcsTestID+".txt"), r.URL.EscapedPath())
					_, err := fmt.Fprint(w, target+"\n")
					require.NoError(t, err)
					return
				}
				require.Equal(t, job.Prefix, r.URL.Query().Get("prefix"))
				require.Equal(t, "/", r.URL.Query().Get("delimiter"))
				require.Empty(t, r.URL.Query().Get("startOffset"), "bootstrap must not truncate history")
				object := gcsObject{Name: job.Prefix + gcsTestID + ".txt", Bucket: gcsTestBucket}
				if metadata {
					object.Metadata = map[string]string{"x-goog-meta-link": target + "/"}
				}
				require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Items: []gcsObject{object, {Name: job.Prefix + "latest-build.txt"}, object}}))
			})
			var got []JobURI
			err := client.ListRuns(t.Context(), job, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), func(uri JobURI) error { got = append(got, uri); return nil })
			require.NoError(t, err)
			require.Equal(t, []JobURI{JobURI(target), JobURI(target)}, got, "deduplication belongs to the controller")
			if metadata {
				require.Zero(t, downloads)
			} else {
				require.Equal(t, 2, downloads)
			}
		})
	}
}

func TestGCSListRunsRejectsInvalidEntries(t *testing.T) {
	alias := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/directory/" + gcsTestJob + "/", Aliases: true}
	direct := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/pull/batch/" + gcsTestJob + "/"}
	target := "gs://" + gcsTestBucket + "/pr-logs/pull/Azure_ARO-HCP/7178/" + gcsTestJob + "/" + gcsTestID
	type testCase struct {
		name string
		job  GCSJob
		page gcsListPage
	}
	tests := []testCase{
		{name: "non-numeric prefix", job: direct, page: gcsListPage{Prefixes: []string{direct.Prefix + "invalid/"}}},
		{name: "short ID", job: direct, page: gcsListPage{Prefixes: []string{direct.Prefix + "123/"}}},
		{name: "overflow", job: direct, page: gcsListPage{Prefixes: []string{direct.Prefix + "9223372036854775808/"}}},
		{name: "wrong prefix", job: direct, page: gcsListPage{Prefixes: []string{"logs/other/" + gcsTestID + "/"}}},
		{name: "recursive prefix", job: direct, page: gcsListPage{Prefixes: []string{direct.Prefix + gcsTestID + "/artifacts/"}}},
		{name: "direct object", job: direct, page: gcsListPage{Items: []gcsObject{{Name: direct.Prefix + gcsTestID + ".txt"}}}},
		{name: "alias prefix", job: alias, page: gcsListPage{Prefixes: []string{alias.Prefix + gcsTestID + "/"}}},
		{name: "wrong object bucket", job: alias, page: gcsListPage{Items: []gcsObject{{Name: alias.Prefix + gcsTestID + ".txt", Bucket: "other-bucket"}}}},
		{name: "wrong object prefix", job: alias, page: gcsListPage{Items: []gcsObject{{Name: "other/" + gcsTestID + ".txt"}}}},
	}
	for name, badTarget := range map[string]string{
		"empty metadata":  "",
		"wrong bucket":    strings.Replace(target, gcsTestBucket, "different-bucket", 1),
		"wrong job":       strings.Replace(target, gcsTestJob, gcsTestJob+"-other", 1),
		"wrong ID":        strings.Replace(target, gcsTestID, "2104421180684374017", 1),
		"wrong repo":      strings.Replace(target, "Azure_ARO-HCP", "other_repo", 1),
		"wrong family":    "gs://" + gcsTestBucket + "/logs/" + gcsTestJob + "/" + gcsTestID,
		"artifact suffix": target + "/artifacts", "traversal": target + "/../" + gcsTestID,
	} {
		tests = append(tests, testCase{name, alias, gcsListPage{Items: []gcsObject{{Name: alias.Prefix + gcsTestID + ".txt", Metadata: map[string]string{"x-goog-meta-link": badTarget}}}}})
	}
	for _, name := range []string{"123.txt", gcsTestID, gcsTestID + ".txt.bak", "wrong.txt", "nested/" + gcsTestID + ".txt", "../" + gcsTestID + ".txt"} {
		tests = append(tests, testCase{name, alias, gcsListPage{Items: []gcsObject{{Name: alias.Prefix + name, Metadata: map[string]string{"x-goog-meta-link": target}}}}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				require.Empty(t, r.URL.Query().Get("alt"), "invalid entries must not trigger downloads")
				require.NoError(t, json.NewEncoder(w).Encode(tt.page))
			})
			err := client.ListRuns(t.Context(), tt.job, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), func(JobURI) error { t.Error("invalid run emitted"); return nil })
			require.Error(t, err)
		})
	}
}

func TestGCSListRunsExtremeWindows(t *testing.T) {
	job := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/pull/batch/" + gcsTestJob + "/"}
	for _, id := range []string{"1000000000000000000", "9223372036854775807"} {
		t.Run(id, func(t *testing.T) {
			stamp, err := BuildIDTime(id)
			require.NoError(t, err)
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if id == "9223372036854775807" {
					require.Equal(t, job.Prefix+"9223372036854775808", r.URL.Query().Get("endOffset"))
				} else {
					require.Equal(t, job.Prefix+"1000000000000000000", r.URL.Query().Get("startOffset"))
				}
				require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Prefixes: []string{job.Prefix + id + "/"}}))
			})
			count := 0
			err = client.ListRuns(t.Context(), job, stamp, stamp, func(JobURI) error { count++; return nil })
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
	client := newGCSFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid/empty window made an HTTP request") })
	emit := func(JobURI) error { t.Error("unexpected run"); return nil }
	old := time.UnixMilli(snowflakeEpochMS)
	require.NoError(t, client.ListRuns(t.Context(), job, time.Time{}, old, emit))
	require.Error(t, client.ListRuns(t.Context(), job, time.Time{}, time.Time{}, emit))
	require.Error(t, client.ListRuns(t.Context(), job, old.Add(time.Hour), old, emit))
	require.Error(t, client.ListRuns(t.Context(), job, time.Time{}, time.UnixMilli(maxSnowflakeMS+1), emit))
	require.Error(t, client.ListRuns(t.Context(), GCSJob{Name: job.Name, Prefix: "logs/other/"}, time.Time{}, old, emit))
	job.Aliases = true
	require.Error(t, client.ListRuns(t.Context(), job, time.Time{}, old, emit))
}

func TestGCSErrorsAndCancellation(t *testing.T) {
	for _, mode := range []string{"status", "json", "token", "prefix", "object"} {
		t.Run(mode, func(t *testing.T) {
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "status":
					w.WriteHeader(http.StatusForbidden)
				case "json":
					_, err := fmt.Fprint(w, "invalid JSON")
					require.NoError(t, err)
				case "token":
					require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{NextPageToken: "repeated"}))
				case "prefix":
					require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Prefixes: []string{"logs/wrong-job/"}}))
				case "object":
					require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Items: []gcsObject{{Name: "unexpected"}}}))
				}
			})
			_, err := client.ListJobs(t.Context())
			require.Error(t, err)
		})
	}
	job := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/pull/batch/" + gcsTestJob + "/"}
	client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Prefixes: []string{job.Prefix + gcsTestID + "/"}, NextPageToken: "must-not-fetch"}))
	})
	sentinel := errors.New("callback failed")
	err := client.ListRuns(t.Context(), job, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), func(JobURI) error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.ListJobs(ctx)
	require.ErrorIs(t, err, context.Canceled)
	client.Bucket = "invalid/bucket"
	_, err = client.ListJobs(t.Context())
	require.Error(t, err)
}

func TestGCSAliasFallbackErrors(t *testing.T) {
	job := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/directory/" + gcsTestJob + "/", Aliases: true}
	for _, body := range []string{
		"", strings.Repeat("x", 4097),
		"gs://other-bucket/pr-logs/pull/Azure_ARO-HCP/7178/" + gcsTestJob + "/" + gcsTestID,
		"gs://" + gcsTestBucket + "/pr-logs/pull/Azure_ARO-HCP/7178/other-job/" + gcsTestID,
		"gs://" + gcsTestBucket + "/pr-logs/pull/Azure_ARO-HCP/7178/" + gcsTestJob + "/2104421180684374017",
		"not found",
	} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("alt") == "media" {
					if body == "not found" {
						w.WriteHeader(http.StatusNotFound)
					}
					_, err := fmt.Fprint(w, body)
					require.NoError(t, err)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Items: []gcsObject{{Name: job.Prefix + gcsTestID + ".txt"}}}))
			})
			err := client.ListRuns(t.Context(), job, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), func(JobURI) error { t.Error("invalid run emitted"); return nil })
			require.Error(t, err)
		})
	}
}

func TestGCSListRunsFractionalBounds(t *testing.T) {
	job := GCSJob{Name: gcsTestJob, Prefix: "pr-logs/pull/batch/" + gcsTestJob + "/"}
	stamp, err := BuildIDTime(gcsTestID)
	require.NoError(t, err)
	since, until := stamp.Add(time.Nanosecond), stamp.Add(time.Millisecond+time.Nanosecond)
	first, err := SnowflakeLowerBound(since)
	require.NoError(t, err)
	end, err := SnowflakeLowerBound(until)
	require.NoError(t, err)
	client := newGCSFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, job.Prefix+first, r.URL.Query().Get("startOffset"))
		require.Equal(t, job.Prefix+end, r.URL.Query().Get("endOffset"))
		require.NoError(t, json.NewEncoder(w).Encode(gcsListPage{Prefixes: []string{job.Prefix + gcsTestID + "/", job.Prefix + first + "/", job.Prefix + end + "/"}}))
	})
	var got []JobURI
	err = client.ListRuns(t.Context(), job, since, until, func(uri JobURI) error { got = append(got, uri); return nil })
	require.NoError(t, err)
	require.Equal(t, []JobURI{JobURI("gs://" + gcsTestBucket + "/" + job.Prefix + first)}, got)
}
