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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

type artifactTransport func(*http.Request) (*http.Response, error)

func (f artifactTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type artifactReadFailure struct{}

func (artifactReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// A skipped test is stamped with start and end times just as a passing one is,
// so an implementation that inferred "ran" from the timestamps would record the
// whole suite. Only the verdict separates them.
func TestTestRowsForKeepsOnlyExecutedTests(t *testing.T) {
	ran := time.Date(2026, 8, 25, 21, 13, 49, 0, time.UTC)

	results := []snapshot.TestResult{
		{Name: "installs a cluster", Result: "passed", StartTime: ran, EndTime: ran.Add(time.Minute)},
		{Name: "skipped on this platform", Result: "skipped", StartTime: ran, EndTime: ran},
		{Name: "deletes a node pool", Result: "failed", Failed: true, StartTime: ran, EndTime: ran.Add(time.Minute)},
		{Name: "[sig-sippy] infrastructure should work", Result: "failed", Failed: true, StartTime: ran, EndTime: ran},
	}

	rows, names := testRowsFor("2092349199822622720", results)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (skipped and Sippy's own tests excluded): %+v", len(rows), rows)
	}
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	for _, row := range rows {
		if row.BuildID != "2092349199822622720" {
			t.Errorf("row is not keyed by the run: %q", row.BuildID)
		}
	}
	if rows[0].Result != "passed" || rows[0].Failed {
		t.Errorf("passing test recorded as %q failed=%v", rows[0].Result, rows[0].Failed)
	}
	if rows[1].Result != "failed" || !rows[1].Failed {
		t.Errorf("failing test recorded as %q failed=%v", rows[1].Result, rows[1].Failed)
	}
}

// Names are the dimension the join table points at, so a run that exercises the
// same test twice must not produce two rows claiming the same id.
func TestTestRowsForEmitsEachNameOnce(t *testing.T) {
	ran := time.Date(2026, 8, 25, 21, 13, 49, 0, time.UTC)
	results := []snapshot.TestResult{
		{Name: "installs a cluster", Result: "passed", StartTime: ran, EndTime: ran},
		{Name: "installs a cluster", Result: "failed", Failed: true, StartTime: ran, EndTime: ran},
	}

	rows, names := testRowsFor("2092349199822622720", results)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want both attempts recorded", len(rows))
	}
	if len(names) != 1 {
		t.Fatalf("got %d names, want 1", len(names))
	}
	if rows[0].TestID != rows[1].TestID || rows[0].TestID != names[0].TestID {
		t.Error("both rows and the name must share one id")
	}
}

// The id has to be reproducible from the name alone: it is computed
// independently on every pass and after every restart, and rows written weeks
// apart must still join.
func TestTestIDForIsStableAndDistinct(t *testing.T) {
	first := testIDFor("installs a cluster")
	if first != testIDFor("installs a cluster") {
		t.Error("the same name must always yield the same id")
	}
	if first == testIDFor("installs a cluster ") {
		t.Error("names differing only in trailing space must not collide")
	}
	if first == "" {
		t.Error("id must not be empty")
	}
}

// A terminal Sippy run may lack finished.json. Missing enrichment leaves a zero
// timestamp; readiness for outcome ingestion is checked separately against Sippy.
func TestFetchFinishedAtToleratesAMissingRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	finishedAt, err := fetchFinishedAtFrom(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("a missing finished.json must not be an error, got %v", err)
	}
	if !finishedAt.IsZero() {
		t.Errorf("got %v, want the zero time", finishedAt)
	}
}

func TestFetchFinishedAtReadsTheCompletionTime(t *testing.T) {
	for _, bucket := range []string{"test-platform-results", "test-platform-results-public"} {
		t.Run(bucket, func(t *testing.T) {
			const prefix = "logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890"
			info, err := snapshot.ParseProwURL("https://prow.ci.openshift.org/view/gs/" + bucket + "/" + prefix)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
				wantURL := "https://storage.googleapis.com/" + bucket + "/" + prefix + "/finished.json"
				if r.Method != http.MethodGet || r.URL.String() != wantURL {
					return nil, fmt.Errorf("unexpected completion request: %s %s", r.Method, r.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"timestamp":1787696844,"passed":false,"result":"ABORTED"}`)),
				}, nil
			})}
			finishedAt, err := fetchFinishedAt(t.Context(), client, info.GCSBucket, info.GCSPrefix)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := time.Unix(1787696844, 0).UTC(); !finishedAt.Equal(want) {
				t.Errorf("got %v, want %v", finishedAt, want)
			}
		})
	}
}

func TestFetchADOBuildID(t *testing.T) {
	for _, bucket := range []string{"test-platform-results", "test-platform-results-public"} {
		for _, tc := range []struct {
			name, body, want, wantErr string
			status                    int
		}{
			{name: "rollout", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":"181589814"}},"status":{"build_id":"2100631679885381632"}}`, want: "181589814"},
			{name: "identifier preserved", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":"00123"}}}`, want: "00123"},
			{name: "no rollout annotation", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/environment":"int"}},"status":{"build_id":"2100631679885381632"}}`},
			{name: "no annotations", status: 200, body: `{"metadata":{}}`},
			{name: "empty annotation", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":""}}}`},
			{name: "missing artifact", status: 404, wantErr: "artifact not found"},
			{name: "forbidden", status: 403, wantErr: "unexpected status 403"},
			{name: "server error", status: 503, wantErr: "unexpected status 503"},
			{name: "malformed JSON", status: 200, body: `{`, wantErr: "failed to parse"},
			{name: "invalid annotation type", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":123}}}`, wantErr: "failed to parse"},
		} {
			t.Run(bucket+"/"+tc.name, func(t *testing.T) {
				const prefix = "logs/branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel/2100631679885381632"
				client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
					wantURL := "https://storage.googleapis.com/" + bucket + "/" + prefix + "/prowjob.json"
					if r.Method != http.MethodGet || r.URL.String() != wantURL {
						return nil, fmt.Errorf("unexpected metadata request: %s %s", r.Method, r.URL)
					}
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				})}
				got, err := fetchADOBuildID(t.Context(), client, bucket, prefix)
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("got error %v, want %q", err, tc.wantErr)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("ADO build ID = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func TestFetchADOBuildIDPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}
	_, err := fetchADOBuildID(ctx, client, "test-platform-results-public", "logs/job/123")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

// artifactClient supplies both GCS listing and object reads through the injected
// HTTP client. Unexpected reads fail, so source independence is checked too.
func artifactClient(t *testing.T, objects map[string]string, statuses map[string]int) *http.Client {
	t.Helper()
	return &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
		key := r.URL.Query().Get("prefix")
		isList := key != ""
		if !isList {
			key = strings.TrimPrefix(r.URL.Path, "/test-platform-results/")
		}
		status := statuses[key]
		if status == -1 {
			return nil, errors.New("network unavailable")
		}
		if status == -2 {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(artifactReadFailure{})}, nil
		}
		if status == 0 {
			status = http.StatusOK
		}
		body, ok := objects[key]
		if !ok && status == http.StatusOK {
			t.Errorf("unexpected artifact request: %s", r.URL)
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

const artifactTestPrefix = "logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/123"
const artifactTestURL = "https://prow.ci.openshift.org/view/gs/test-platform-results/" + artifactTestPrefix
const artifactTestRoot = artifactTestPrefix + "/artifacts/e2e-parallel/"

func artifactListing(paths ...string) string {
	items := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		items = append(items, map[string]string{"name": path})
	}
	data, _ := json.Marshal(map[string]any{"items": items})
	return string(data)
}

func TestFetchE2ERowsStrict(t *testing.T) {
	const resultPrefix = artifactTestRoot + "aro-hcp-test-persistent/artifacts/extension_test_result_e2e_"
	const timingPrefix = artifactTestRoot + "aro-hcp-gather-test-visualization/artifacts/test-timing/timing-metadata-"
	const results = `[{"name":"test name","result":"failed","error":"failure detail","output":"\"resourceGroup\"=\"test-rg\"","startTime":"2026-01-01 00:00:00.000000 UTC","endTime":"2026-01-01 01:00:00.000000 UTC"},{"name":"skip","result":"skipped"},{"name":"[sig-sippy] synthetic","result":"failed"}]`
	for _, tc := range []struct {
		name, target, body string
		status             int
		wantRows           int
		wantErr            bool
		wantNamesOnError   bool
		wantEmpty          bool
		wantProblem        string
	}{
		{name: "complete", wantRows: 1},
		{name: "missing directory", target: artifactTestPrefix + "/artifacts/", body: `{}`},
		{name: "directory 404", target: artifactTestPrefix + "/artifacts/", status: 404},
		{name: "directory network", target: artifactTestPrefix + "/artifacts/", status: -1, wantErr: true},
		{name: "directory 429", target: artifactTestPrefix + "/artifacts/", status: 429, wantErr: true},
		{name: "directory 503", target: artifactTestPrefix + "/artifacts/", status: 503, wantErr: true},
		{name: "empty results", target: resultPrefix, body: `{}`, wantProblem: "e2e/absent"},
		{name: "valid empty report", target: resultPrefix + "1.json", body: `[]`, wantEmpty: true},
		{name: "valid filtered report", target: resultPrefix + "1.json", body: `[{"name":"skip","result":"skipped"},{"name":"[sig-sippy] synthetic","result":"failed"}]`, wantEmpty: true},
		{name: "result list transient", target: resultPrefix, status: 503, wantErr: true},
		{name: "malformed primary discards earlier results", target: resultPrefix + "2.json", body: `[`},
		{name: "null primary entry", target: resultPrefix + "2.json", body: `[null]`},
		{name: "null primary file", target: resultPrefix + "2.json", body: `null`},
		{name: "empty primary entry", target: resultPrefix + "2.json", body: `[{}]`, wantProblem: "e2e/malformed"},
		{name: "missing name", target: resultPrefix + "2.json", body: `[{"result":"passed"}]`, wantProblem: "e2e/malformed"},
		{name: "empty name", target: resultPrefix + "2.json", body: `[{"name":"","result":"failed"}]`, wantProblem: "e2e/malformed"},
		{name: "blank name", target: resultPrefix + "2.json", body: `[{"name":"  ","result":"skipped"}]`, wantProblem: "e2e/malformed"},
		{name: "missing verdict", target: resultPrefix + "2.json", body: `[{"name":"test"}]`, wantProblem: "e2e/malformed"},
		{name: "empty verdict", target: resultPrefix + "2.json", body: `[{"name":"test","result":""}]`, wantProblem: "e2e/malformed"},
		{name: "invalid verdict", target: resultPrefix + "2.json", body: `[{"name":"test","result":"unknown"}]`, wantProblem: "e2e/malformed"},
		{name: "case sensitive verdict", target: resultPrefix + "2.json", body: `[{"name":"test","result":"Passed"}]`, wantProblem: "e2e/malformed"},
		{name: "missing primary", target: resultPrefix + "2.json", status: 404},
		{name: "primary transient", target: resultPrefix + "2.json", status: 503, wantErr: true},
		{name: "primary network", target: resultPrefix + "2.json", status: -1, wantErr: true},
		{name: "primary interrupted body", target: resultPrefix + "2.json", status: -2, wantErr: true},
		{name: "timing list transient", target: timingPrefix, status: 429, wantErr: true, wantNamesOnError: true},
		{name: "timing read transient", target: timingPrefix + "test.yaml", status: 503, wantErr: true, wantNamesOnError: true},
		{name: "timing interrupted body", target: timingPrefix + "test.yaml", status: -2, wantErr: true, wantNamesOnError: true},
		{name: "timing list missing", target: timingPrefix, status: 404, wantRows: 1},
		{name: "timing list empty", target: timingPrefix, body: `{}`, wantRows: 1},
		{name: "timing missing", target: timingPrefix + "test.yaml", status: 404, wantRows: 1, wantProblem: "timing/absent"},
		{name: "timing empty", target: timingPrefix + "test.yaml", wantRows: 1},
		{name: "timing malformed", target: timingPrefix + "test.yaml", body: `[:`, wantRows: 1, wantProblem: "timing/malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]string{
				artifactTestPrefix + "/artifacts/": `{"prefixes":["` + artifactTestRoot + `","` + artifactTestPrefix + `/artifacts/parallel/"]}`,
				resultPrefix:                       artifactListing(resultPrefix+"1.json", resultPrefix+"2.json"),
				resultPrefix + "1.json":            results,
				resultPrefix + "2.json":            `[]`,
				timingPrefix:                       artifactListing(timingPrefix + "test.yaml"),
				timingPrefix + "test.yaml":         "identifier: [test, name]\nfinishedAt: '2026-01-01T00:50:00Z'\nsteps:\n- name: identity container setup\n  finishedAt: '2026-01-01T00:05:00Z'\n- name: exercise test\n  startedAt: '2026-01-01T00:06:00Z'\n",
			}
			if tc.target != "" {
				objects[tc.target] = tc.body
			}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
			rows, names, err := fetchE2ERows(ctx, artifactClient(t, objects, map[string]int{tc.target: tc.status}), artifactTestURL)
			if tc.wantProblem != "" && (len(problems) != 1 || problems[0] != tc.wantProblem) {
				t.Fatalf("problems=%v, want %s", problems, tc.wantProblem)
			}
			if (tc.wantErr || tc.wantEmpty || tc.name == "complete") && len(problems) != 0 {
				t.Fatalf("unexpected permanent problem: %v", problems)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			wantNames := tc.wantRows
			if tc.wantNamesOnError {
				wantNames = 1
				if len(names) != 1 || names[0].Name != "test name" || names[0].TestID != testIDFor("test name") {
					t.Fatalf("lost names on enrichment error: %+v", names)
				}
			}
			if len(rows) != tc.wantRows || len(names) != wantNames {
				t.Fatalf("rows=%+v names=%+v, want %d", rows, names, tc.wantRows)
			}
			if (rows != nil) != (tc.wantRows > 0 || tc.wantEmpty) || (names != nil) != (wantNames > 0 || tc.wantEmpty) {
				t.Fatalf("nil/empty contract violated: rows=%#v names=%#v", rows, names)
			}
			if len(rows) == 0 {
				return
			}
			row := rows[0]
			if row.Message != "failure detail" || row.ResourceGroup != "test-rg" || row.Result != "failed" || !row.Failed || row.BuildID != "123" || row.TestID != testIDFor("test name") || row.StartedAt.IsZero() || row.FinishedAt.IsZero() {
				t.Fatalf("derived fields changed: %+v", row)
			}
			if tc.name == "complete" {
				if row.SetupFinishTime.Minute() != 5 || row.TestStartTime.Minute() != 6 || row.CleanupStartTime.Minute() != 50 {
					t.Fatalf("timing boundaries = %+v", row)
				}
			} else if !row.SetupFinishTime.IsZero() || !row.TestStartTime.IsZero() || !row.CleanupStartTime.IsZero() {
				t.Fatalf("absent timing should be zero: %+v", row)
			}
		})
	}
}

func TestE2ETimingLoggerRetainsSourceContext(t *testing.T) {
	const resultPrefix = artifactTestRoot + "aro-hcp-test-persistent/artifacts/extension_test_result_e2e_"
	const timingPrefix = artifactTestRoot + "aro-hcp-gather-test-visualization/artifacts/test-timing/timing-metadata-"
	objects := map[string]string{
		artifactTestPrefix + "/artifacts/": `{"prefixes":["` + artifactTestRoot + `"]}`,
		resultPrefix:                       artifactListing(resultPrefix + "1.json"),
		resultPrefix + "1.json":            `[{"name":"test","result":"failed"}]`,
		timingPrefix:                       artifactListing(timingPrefix + "test.yaml"),
		timingPrefix + "test.yaml":         `[:`,
	}
	var logs []string
	logger := funcr.New(func(_, message string) { logs = append(logs, message) }, funcr.Options{Verbosity: 1}).WithValues("controller_name", "test-controller")
	ctx := utils.ContextWithLogger(t.Context(), logger)
	rows, _, err := fetchE2ERows(ctx, artifactClient(t, objects, nil), artifactTestURL)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for _, message := range logs {
		if strings.Contains(message, "Failed to unmarshal timing metadata") {
			if !strings.Contains(message, `"source"="e2e"`) || !strings.Contains(message, `"controller_name"="test-controller"`) {
				t.Fatalf("timing log lost context: %s", message)
			}
			return
		}
	}
	t.Fatalf("missing timing diagnostic: %v", logs)
}

func TestFetchJobOutcomeDetailDoesNotReadTestSources(t *testing.T) {
	for _, body := range []string{`{"metadata":{"annotations":{"ev2.rollout/build":"00123"}}}`, `{`} {
		objects := map[string]string{
			artifactTestPrefix + "/prowjob.json":  body,
			artifactTestPrefix + "/finished.json": `{"timestamp":1787696844}`,
		}
		detail, err := fetchJobOutcomeDetail(t.Context(), artifactClient(t, objects, nil), artifactTestURL)
		if err != nil || detail.FinishedAt.IsZero() {
			t.Fatalf("detail=%+v err=%v", detail, err)
		}
		if body != "{" && detail.ADOBuildID != "00123" {
			t.Fatalf("lost build ID: %+v", detail)
		}
	}
}

func TestFetchJobOutcomeDetailPRConfig(t *testing.T) {
	const prefix = "pr-logs/pull/Azure_ARO-HCP/1/pull-ci-Azure-ARO-HCP-main-e2e-parallel/123"
	const root = prefix + "/artifacts/e2e-parallel/"
	const configPath = root + "aro-hcp-provision-environment/artifacts/config.yaml"
	const fallbackPath = root + "aro-hcp-hypershift-deploy/artifacts/config.yaml"
	for _, tc := range []struct {
		name, config                    string
		status                          int
		fallback, wantErr, wantClusters bool
	}{
		{name: "normal", config: "svc: {aks: {name: svc}}\nmgmt: {aks: {name: mgmt}}", wantClusters: true},
		{name: "fallback", status: 404, fallback: true, wantClusters: true},
		{name: "absent", status: 404, fallback: true},
		{name: "malformed", config: "[:"},
		{name: "transient", status: 503, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]string{
				prefix + "/prowjob.json":  `{}`,
				prefix + "/finished.json": `{}`,
				prefix + "/artifacts/":    `{"prefixes":["` + root + `"]}`,
				configPath:                tc.config,
			}
			statuses := map[string]int{configPath: tc.status}
			if tc.fallback {
				if tc.wantClusters {
					objects[fallbackPath] = "svc: {aks: {name: svc}}\nmgmt: {aks: {name: mgmt}}"
				} else {
					statuses[fallbackPath] = 404
				}
			}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
			detail, err := fetchJobOutcomeDetail(ctx, artifactClient(t, objects, statuses), "https://prow.ci.openshift.org/view/gs/test-platform-results/"+prefix)
			if tc.name == "absent" || tc.name == "malformed" {
				if len(problems) != 1 || problems[0] != "config/"+tc.name {
					t.Fatalf("problems=%v, want config/%s", problems, tc.name)
				}
			} else if len(problems) != 0 {
				t.Fatalf("unexpected permanent problem: %v", problems)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if tc.wantClusters {
				if detail.SvcCluster != "svc" || detail.MgmtCluster != "mgmt" {
					t.Fatalf("detail=%+v", detail)
				}
			} else if detail.SvcCluster != "" || detail.MgmtCluster != "" {
				t.Fatalf("unexpected enrichment: %+v", detail)
			}
		})
	}
}

func TestJobArtifactProblemReporting(t *testing.T) {
	for _, filename := range []string{"prowjob.json", "finished.json"} {
		for _, reason := range []string{"absent", "malformed", "transient"} {
			t.Run(filename+"/"+reason, func(t *testing.T) {
				objects := map[string]string{
					artifactTestPrefix + "/prowjob.json":  `{}`,
					artifactTestPrefix + "/finished.json": `{}`,
				}
				path := artifactTestPrefix + "/" + filename
				statuses := map[string]int{}
				switch reason {
				case "absent":
					statuses[path] = 404
				case "malformed":
					objects[path] = `{`
				case "transient":
					statuses[path] = 503
				}
				var problems []string
				ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
				_, err := fetchJobOutcomeDetail(ctx, artifactClient(t, objects, statuses), artifactTestURL)
				if (err != nil) != (reason == "transient") {
					t.Fatalf("unexpected error: %v", err)
				}
				if reason == "transient" {
					if len(problems) != 0 {
						t.Fatalf("transient reported as permanent: %v", problems)
					}
				} else if len(problems) != 1 || problems[0] != "job/"+reason {
					t.Fatalf("problems=%v, want job/%s", problems, reason)
				}
			})
		}
	}
}
