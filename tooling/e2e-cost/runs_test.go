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
	"net/http"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// gcsFake serves GCS JSON listings (prefix, delimiter, matchGlob) and object
// reads from an in-memory object set with creation times.
func gcsFake(t *testing.T, objects map[string]string, created map[string]string) *http.Client {
	t.Helper()
	var names []string
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	return &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/storage/v1/b/"+discoveryBucket+"/o" {
			body, ok := objects[strings.TrimPrefix(r.URL.Path, "/"+discoveryBucket+"/")]
			if !ok {
				t.Fatalf("unexpected object read %s", r.URL.Path)
			}
			return billingResponse(200, body), nil
		}
		q := r.URL.Query()
		prefix, delimiter, glob := q.Get("prefix"), q.Get("delimiter"), q.Get("matchGlob")
		var items []map[string]string
		prefixes := map[string]bool{}
		for _, name := range names {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rest := strings.TrimPrefix(name, prefix)
			if delimiter != "" && strings.Contains(rest, delimiter) {
				prefixes[prefix+rest[:strings.Index(rest, delimiter)+1]] = true
				continue
			}
			if glob != "" && !gcsGlob(glob, name) {
				continue
			}
			items = append(items, map[string]string{"name": name, "timeCreated": created[name]})
		}
		var list []string
		for p := range prefixes {
			list = append(list, p)
		}
		body, _ := json.Marshal(map[string]any{"items": items, "prefixes": list})
		return billingResponse(200, string(body)), nil
	})}
}

// gcsGlob supports the patterns ListRuns uses: *, ** and {a,b}.
func gcsGlob(pattern, name string) bool {
	if open := strings.Index(pattern, "{"); open >= 0 {
		end := strings.Index(pattern, "}")
		for _, alt := range strings.Split(pattern[open+1:end], ",") {
			if gcsGlob(pattern[:open]+alt+pattern[end+1:], name) {
				return true
			}
		}
		return false
	}
	if i := strings.Index(pattern, "**"); i >= 0 {
		// Match the head (which may hold single-segment wildcards) against the
		// same number of leading path segments.
		head := pattern[:i]
		segments := strings.SplitAfter(name, "/")
		n := strings.Count(head, "/")
		if len(segments) <= n {
			return false
		}
		ok, _ := path.Match(head, strings.Join(segments[:n], ""))
		return ok
	}
	ok, _ := path.Match(pattern, name)
	return ok
}

func TestListRunsFromJobHistory(t *testing.T) {
	const day = "2026-10-19T12:00:00Z"
	dev := "pr-logs/pull/Azure_ARO-HCP/7/" + discoveryJob + "/"
	prod := "logs/periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel/"
	images := "pr-logs/pull/Azure_ARO-HCP/7/pull-ci-Azure-ARO-HCP-main-e2e-images/"
	objects := map[string]string{
		// Dev presubmit: two builds that day and one the day before.
		"pr-logs/directory/" + discoveryJob + "/1000.txt":         "gs://" + discoveryBucket + "/" + dev + "1000\n",
		"pr-logs/directory/" + discoveryJob + "/999.txt":          "gs://" + discoveryBucket + "/" + dev + "999\n",
		"pr-logs/directory/" + discoveryJob + "/500.txt":          "gs://" + discoveryBucket + "/" + dev + "500\n",
		"pr-logs/directory/" + discoveryJob + "/latest-build.txt": "1000",
		// Periodic prod job testing a persistent environment.
		prod + "42/started.json": "{}",
		// Image build job: e2e in its name but not e2e-parallel, so it is excluded.
		"pr-logs/directory/pull-ci-Azure-ARO-HCP-main-e2e-images/3.txt": "gs://" + discoveryBucket + "/" + images + "3\n",
		// Unit job: no e2e in its name.
		"pr-logs/directory/pull-ci-Azure-ARO-HCP-main-unit/4.txt": "gs://" + discoveryBucket + "/pr-logs/pull/Azure_ARO-HCP/7/pull-ci-Azure-ARO-HCP-main-unit/4\n",
	}
	created := map[string]string{}
	for name := range objects {
		created[name] = day
	}
	created["pr-logs/directory/"+discoveryJob+"/500.txt"] = "2026-10-18T23:59:59Z"
	runs, err := ListRuns(context.Background(), gcsFake(t, objects, created), time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	view := "https://prow.ci.openshift.org/view/gs/" + discoveryBucket + "/"
	want := []Run{
		{Job: "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel", URL: view + prod + "42"},
		{Job: discoveryJob, URL: view + dev + "999"},
		{Job: discoveryJob, URL: view + dev + "1000"},
	}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("unexpected runs:\n%+v\nwant\n%+v", runs, want)
	}
}

func TestDiscoveryBuildPaths(t *testing.T) {
	for _, tc := range []struct {
		prefix, job, pr, build string
		ok                     bool
	}{
		{"pr-logs/pull/Azure_ARO-HCP/7/" + discoveryJob + "/12/", discoveryJob, "7", "12", true},
		{"pr-logs/pull/Azure_ARO-HCP/7/pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel/12", "pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel", "7", "12", true},
		{"logs/periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel/12/", "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel", "", "12", true},
		{"logs/branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel/12", "branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel", "", "12", true},
		{"logs/periodic-ci-other-repo-e2e-parallel/12/", "", "", "", false},
		{"pr-logs/pull/Azure_ARO-HCP/7/pull-ci-Azure-ARO-HCP-main-e2e-images/12/", "", "", "", false},
		{"pr-logs/pull/Azure_ARO-HCP/7/pull-ci-Azure-ARO-HCP-main-unit/12/", "", "", "", false},
	} {
		job, pr, build, ok := discoveryBuild(tc.prefix)
		if job != tc.job || pr != tc.pr || build != tc.build || ok != tc.ok {
			t.Fatalf("discoveryBuild(%q) = %q %q %q %t", tc.prefix, job, pr, build, ok)
		}
	}
}

func TestBaseRefCommit(t *testing.T) {
	const sha = "29bcc9c3f49ccafc90760a20a427a44b96f4c69a"
	decode := func(raw string) any {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct{ name, prowjob, want string }{
		{"postsubmit refs", `{"spec":{"refs":{"org":"Azure","repo":"ARO-HCP","base_ref":"main","base_sha":"` + sha + `"}}}`, sha},
		{"periodic extra_refs", `{"spec":{"extra_refs":[{"org":"other","repo":"x","base_sha":"` + strings.Repeat("a", 40) + `"},{"org":"Azure","repo":"ARO-HCP","base_sha":"` + sha + `"}]}}`, sha},
		{"periodic without SHA", `{"spec":{"extra_refs":[{"org":"Azure","repo":"ARO-HCP","base_ref":"main"}]}}`, ""},
		{"other repository", `{"spec":{"refs":{"org":"openshift","repo":"release","base_sha":"` + sha + `"}}}`, ""},
	} {
		if got := baseRefCommit(decode(tc.prowjob)); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildTestArtifactPrefix(t *testing.T) {
	const prefix = "logs/periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel/12/"
	s := &Snapshot{}
	got, provisioned := buildTestArtifactPrefix(s, "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel", prefix, []string{
		prefix + "artifacts/prod-e2e-parallel/aro-hcp-test-persistent/artifacts/junit.xml",
		prefix + "artifacts/prod-e2e-parallel/aro-hcp-lease-acquire/build-log.txt",
	})
	if got != "artifacts/prod-e2e-parallel/aro-hcp-test-persistent/artifacts/" || provisioned || len(s.Diagnostics) != 0 {
		t.Fatalf("persistent build: %q provisioned=%t %+v", got, provisioned, s.Diagnostics)
	}
	if got, provisioned := buildTestArtifactPrefix(s, discoveryJob, prefix, nil); got != devTestArtifactPrefix || !provisioned {
		t.Fatal("a dev build without test artifacts must keep the dev prefix so missing artifacts are reported")
	}
	if got, _ := buildTestArtifactPrefix(s, "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel", prefix, nil); got != "" || !hasDiscoveryDiagnostic(s, "unsupported-job") {
		t.Fatal("a non-dev build without a test step is unsupported")
	}
	if got, _ := buildTestArtifactPrefix(s, discoveryJob, prefix, []string{
		prefix + "artifacts/a/aro-hcp-test-local/artifacts/x", prefix + "artifacts/b/aro-hcp-test-persistent/artifacts/x",
	}); got != "" || !hasDiscoveryDiagnostic(s, "ambiguous-test-step") {
		t.Fatal("two test steps are ambiguous")
	}
}
