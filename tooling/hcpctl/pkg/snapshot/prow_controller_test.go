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

package snapshot

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type controllerArtifactTransport func(*http.Request) (*http.Response, error)

func (f controllerArtifactTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProwControllerStrictAndSnapshotBestEffort(t *testing.T) {
	const root = "logs/job-e2e/123/artifacts/e2e/"
	const resultPrefix = root + "aro-hcp-test-persistent/artifacts/extension_test_result_e2e_"
	const timingPrefix = root + timingMetadataPath + "timing-metadata-"
	for _, failure := range []string{"result download", "result malformed", "result missing fields", "timing list", "timing download"} {
		for _, strict := range []bool{false, true} {
			mode := "snapshot"
			if strict {
				mode = "controller"
			}
			t.Run(failure+"/"+mode, func(t *testing.T) {
				client := &http.Client{Transport: controllerArtifactTransport(func(r *http.Request) (*http.Response, error) {
					key := r.URL.Query().Get("prefix")
					if key == "" {
						key = strings.TrimPrefix(r.URL.Path, "/bucket/")
					}
					status, body := http.StatusOK, ""
					switch key {
					case "logs/job-e2e/123/artifacts/":
						body = `{"prefixes":["` + root + `"]}`
					case resultPrefix:
						body = `{"items":[{"name":"` + resultPrefix + `1.json"},{"name":"` + resultPrefix + `2.json"}]}`
					case resultPrefix + "1.json":
						body = `[{"name":"test","result":"failed"}]`
					case resultPrefix + "2.json":
						body = `[]`
						if failure == "result download" {
							status = http.StatusServiceUnavailable
						}
						if failure == "result malformed" {
							body = `[`
						}
						if failure == "result missing fields" {
							body = `[{}]`
						}
					case timingPrefix:
						body = `{"items":[{"name":"` + timingPrefix + `test.yaml"}]}`
						if failure == "timing list" {
							status = http.StatusServiceUnavailable
						}
					case timingPrefix + "test.yaml":
						body = `identifier: [test]`
						if failure == "timing download" {
							status = http.StatusServiceUnavailable
						}
					default:
						t.Fatalf("unexpected request %s", r.URL)
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})}
				gcs, err := controllerGCSClient(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				defer gcs.Close()
				var strictClient *http.Client
				if strict {
					strictClient = client
				}
				var problems []string
				ctx := WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
				results, err := fetchProwJobTestResults(ctx, gcs, &ProwJobInfo{GCSBucket: "bucket", GCSPrefix: "logs/job-e2e/123", JobName: "job-e2e"}, strictClient)
				if strict {
					wantResults := 0
					if strings.HasPrefix(failure, "timing ") {
						wantResults = 1
					}
					malformed := failure == "result malformed" || failure == "result missing fields"
					if len(results) != wantResults || (err != nil) == malformed {
						t.Fatalf("strict results=%+v err=%v", results, err)
					}
					if malformed && (len(problems) != 1 || problems[0] != "e2e/malformed") {
						t.Fatalf("problems=%v, want e2e/malformed", problems)
					}
					if wantResults > 0 && (results[0].Name != "test" || !results[0].SetupFinishTime.IsZero() || !results[0].TestStartTime.IsZero() || !results[0].CleanupStartTime.IsZero()) {
						t.Fatalf("expected unenriched results on timing failure: %+v", results)
					}
				} else {
					wantResults := 1
					if failure == "result missing fields" {
						wantResults = 2
					}
					if err != nil || len(results) != wantResults || results[0].Name != "test" || len(problems) != 0 {
						t.Fatalf("best-effort results=%+v err=%v problems=%v", results, err, problems)
					}
				}
			})
		}
	}
}

func TestProwArtifactProblemHandlerBoundedCategories(t *testing.T) {
	var problems []string
	ctx := WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
	for _, source := range []string{"e2e", "timing", "config", "observability", "job"} {
		for _, reason := range []string{"absent", "malformed"} {
			ReportProwArtifactProblem(ctx, source, reason)
		}
	}
	ReportProwArtifactProblem(ctx, "arbitrary-job-name", "absent")
	ReportProwArtifactProblem(ctx, "job", "arbitrary-error")
	if len(problems) != 10 {
		t.Fatalf("unexpected categories: %v", problems)
	}
	ReportProwArtifactProblem(t.Context(), "job", "absent")
	ReportProwArtifactProblem(WithProwArtifactProblemHandler(t.Context(), nil), "job", "absent")
}
