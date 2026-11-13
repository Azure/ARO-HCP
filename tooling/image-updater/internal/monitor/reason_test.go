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

package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// realJUNIT is a trimmed copy of the junit_operator.xml produced by a failed
// image-updater run (build 2102759945211482112): the CSI color payloads
// arrive without ESC bytes (raw ESC is illegal in XML), plus make noise and
// a JSON error payload.
const realJUNIT = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="step graph" tests="9" skipped="0" failures="1" time="1213.6">
    <testcase name="Build image aro-hcp-automation-image-update from the repository" time="923.7">
    </testcase>
    <testcase name="Run multi-stage step aro-hcp-automation-image-update" time="3">
      <failure message="">make[1]: Entering directory '/opt/app-root/src/github.com/Azure/ARO-HCP/tooling/image-updater'
&#129;&quot;[37m[14:20:38.819][0m[91mERROR:[0m[97mcommand failed[0m[90m{&quot;err&quot;: &quot;failed to fetch latest value for aksCommandRuntime: tag master.210916.2 has no creation timestamp&quot;}[0m
make[1]: *** [Makefile:25: update] Error 1</failure>
    </testcase>
  </testsuite>
</testsuites>`

func TestClassifyJUnitExtractsStepAndErrorLine(t *testing.T) {
	reason, err := classifyJUnit([]byte(realJUNIT))
	if err != nil {
		t.Fatalf("classifyJUnit: %v", err)
	}
	want := `Run multi-stage step aro-hcp-automation-image-update: ERROR:command failed{"err": "failed to fetch latest value for aksCommandRuntime: tag master.210916.2 has no creation timestamp"}`
	if reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

func TestClassifyJUnitNoFailures(t *testing.T) {
	reason, err := classifyJUnit([]byte(`<?xml version="1.0"?>
<testsuites><testsuite name="step graph" tests="1" failures="0">
  <testcase name="all good" time="1"></testcase>
</testsuite></testsuites>`))
	if err != nil {
		t.Fatalf("classifyJUnit: %v", err)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty (no failing step)", reason)
	}
}

func TestClassifyJUnitTruncatesLongReasons(t *testing.T) {
	long := `<?xml version="1.0"?>
<testsuites><testsuite name="step graph" tests="1" failures="1">
  <testcase name="step" time="1"><failure message="">` +
		repeatString("x", 500) + ` error line</failure></testcase>
</testsuite></testsuites>`
	reason, err := classifyJUnit([]byte(long))
	if err != nil {
		t.Fatalf("classifyJUnit: %v", err)
	}
	if got := len([]rune(reason)); got > 200 {
		t.Fatalf("reason length = %d runes, want <= 200", got)
	}
}

func TestClassifyJUnitMalformedXML(t *testing.T) {
	if _, err := classifyJUnit([]byte("not xml at all")); err == nil {
		t.Fatal("classifyJUnit succeeded on malformed XML, want error")
	}
}

func TestGCSReasonClientFetchesJUnitForBuild(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Query().Get("alt") != "media" {
			t.Errorf("query alt = %q, want media", r.URL.Query().Get("alt"))
		}
		_, _ = w.Write([]byte(realJUNIT))
	}))
	defer server.Close()

	client := &GCSReasonClient{
		baseURL: server.URL,
		bucket:  DefaultResultsBucket,
		http:    server.Client(),
	}
	reason := client.FailureReason(context.Background(), "periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling", "42")
	if reason == "" {
		t.Fatal("reason empty, want classified failure reason")
	}
	wantPath := "/storage/v1/b/test-platform-results-public/o/logs/periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling/42/artifacts/junit_operator.xml"
	if gotPath != wantPath {
		t.Fatalf("request path = %q, want %q", gotPath, wantPath)
	}
}

func TestGCSReasonClientMissingArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &GCSReasonClient{baseURL: server.URL, http: server.Client()}
	reason := client.FailureReason(context.Background(), "some-job", "42")
	if reason != "" {
		t.Fatalf("reason = %q, want empty on missing artifact", reason)
	}
}

func TestGCSReasonClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	client := &GCSReasonClient{baseURL: server.URL, http: &http.Client{Timeout: 50 * time.Millisecond}}
	reason := client.FailureReason(context.Background(), "some-job", "42")
	if reason != "" {
		t.Fatalf("reason = %q, want empty on timeout", reason)
	}
}

func repeatString(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
