// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Run is one e2e build selected for a day's report.
type Run struct {
	Job string
	URL string // Public Prow URL.
}

// ListRuns returns the builds of every ARO-HCP e2e-parallel job (presubmit,
// periodic, and postsubmit) that started on the given UTC day. Jobs come from
// Prow's history by name rather than a fixed list, so new environments and
// variants are picked up as they appear.
func ListRuns(ctx context.Context, client *http.Client, day time.Time) ([]Run, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	var runs []Run
	for _, family := range []struct{ dir, jobPrefix string }{
		{"pr-logs/directory/", "pull-ci-Azure-ARO-HCP-main-"},
		{"logs/", "periodic-ci-Azure-ARO-HCP-main-"},
		{"logs/", "branch-ci-Azure-ARO-HCP-main-"},
	} {
		jobs, err := listGCSPrefixes(ctx, client, family.dir+family.jobPrefix)
		if err != nil {
			return nil, err
		}
		for _, jobDir := range jobs {
			job := strings.TrimSuffix(strings.TrimPrefix(jobDir, family.dir), "/")
			if !strings.Contains(job, "e2e-parallel") {
				continue
			}
			builds, err := dayBuilds(ctx, client, family.dir, job, start, end)
			if err != nil {
				return nil, err
			}
			for _, build := range builds {
				runs = append(runs, Run{Job: job, URL: "https://prow.ci.openshift.org/view/gs/" + discoveryBucket + "/" + strings.TrimSuffix(build, "/")})
			}
		}
	}
	sort.Slice(runs, func(a, b int) bool {
		if runs[a].Job != runs[b].Job {
			return runs[a].Job < runs[b].Job
		}
		x, y := runs[a].URL, runs[b].URL
		if len(x) != len(y) {
			return len(x) < len(y)
		}
		return x < y
	})
	return runs, nil
}

// dayBuilds returns the artifact prefixes of a job's builds that started in
// [start, end). Presubmits have one pointer object per build in the job
// directory; periodic and branch builds have their own started.json.
func dayBuilds(ctx context.Context, client *http.Client, dir, job string, start, end time.Time) ([]string, error) {
	jobDir := dir + job + "/"
	glob := jobDir + "*/started.json"
	if dir == "pr-logs/directory/" {
		glob = jobDir + "*.txt"
	}
	objects, err := listGCSObjects(ctx, client, jobDir, glob)
	if err != nil {
		return nil, err
	}
	var builds []string
	for _, object := range objects {
		if object.TimeCreated.Before(start) || !object.TimeCreated.Before(end) || strings.HasSuffix(object.Name, "/latest-build.txt") {
			continue
		}
		build := strings.TrimSuffix(object.Name, "started.json")
		if dir == "pr-logs/directory/" {
			body, err := getDiscoveryArtifact(ctx, client, discoveryObjectURL(object.Name))
			if err != nil {
				return nil, fmt.Errorf("read build pointer %s: %w", object.Name, err)
			}
			// Pointers hold gs://<bucket>/<build prefix>.
			build = strings.TrimPrefix(strings.TrimSpace(string(body)), "gs://"+discoveryBucket+"/")
		}
		if _, _, _, ok := discoveryBuild(build); !ok {
			return nil, fmt.Errorf("build %s is not a public ARO-HCP e2e build", object.Name)
		}
		builds = append(builds, strings.TrimSuffix(build, "/")+"/")
	}
	return builds, nil
}

type gcsObject struct {
	Name        string    `json:"name"`
	TimeCreated time.Time `json:"timeCreated"`
}

func listGCSObjects(ctx context.Context, client *http.Client, prefix, glob string) ([]gcsObject, error) {
	var objects []gcsObject
	err := listGCS(ctx, client, url.Values{"prefix": {prefix}, "matchGlob": {glob}, "fields": {"items(name,timeCreated),nextPageToken"}}, func(body []byte) error {
		var page struct {
			Items []gcsObject `json:"items"`
		}
		if json.Unmarshal(body, &page) != nil {
			return fmt.Errorf("invalid GCS object listing")
		}
		objects = append(objects, page.Items...)
		return nil
	})
	return objects, err
}

func listGCSPrefixes(ctx context.Context, client *http.Client, prefix string) ([]string, error) {
	var prefixes []string
	err := listGCS(ctx, client, url.Values{"prefix": {prefix}, "delimiter": {"/"}, "fields": {"prefixes,nextPageToken"}}, func(body []byte) error {
		var page struct {
			Prefixes []string `json:"prefixes"`
		}
		if json.Unmarshal(body, &page) != nil {
			return fmt.Errorf("invalid GCS prefix listing")
		}
		prefixes = append(prefixes, page.Prefixes...)
		return nil
	})
	sort.Strings(prefixes)
	return prefixes, err
}

func listGCS(ctx context.Context, client *http.Client, q url.Values, page func([]byte) error) error {
	q.Set("maxResults", "1000")
	seen := map[string]bool{}
	for pages := 0; pages < 1000; pages++ {
		body, err := getDiscoveryArtifact(ctx, client, "https://storage.googleapis.com/storage/v1/b/"+discoveryBucket+"/o?"+q.Encode())
		if err != nil {
			return err
		}
		var next struct {
			Next string `json:"nextPageToken"`
		}
		if err := page(body); err != nil {
			return err
		}
		if json.Unmarshal(body, &next) != nil || next.Next == "" {
			return nil
		}
		if seen[next.Next] {
			return fmt.Errorf("GCS listing repeated a page token")
		}
		seen[next.Next] = true
		q.Set("pageToken", next.Next)
	}
	return fmt.Errorf("GCS listing exceeds 1000 pages")
}
