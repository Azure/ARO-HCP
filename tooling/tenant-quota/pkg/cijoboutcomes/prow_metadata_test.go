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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

func TestFetchProwCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantResult, problem string
		status                          int
		wantErr                         bool
	}{
		{name: "success", body: `{"timestamp":1787696844,"result":"SUCCESS","passed":false}`, wantResult: "SUCCESS"},
		{name: "failure", body: `{"timestamp":1787696844,"result":"FAILURE","passed":true}`, wantResult: "FAILURE"},
		{name: "aborted", body: `{"timestamp":1787696844,"result":"ABORTED"}`, wantResult: "ABORTED"},
		{name: "error", body: `{"timestamp":1787696844,"result":"ERROR"}`, wantResult: "ERROR"},
		{name: "lowercase success", body: `{"timestamp":1787696844,"result":"success","passed":false}`, wantResult: "SUCCESS"},
		{name: "lowercase failure", body: `{"timestamp":1787696844,"result":"failure","passed":true}`, wantResult: "FAILURE"},
		{name: "lowercase aborted", body: `{"timestamp":1787696844,"result":"aborted"}`, wantResult: "ABORTED"},
		{name: "lowercase error", body: `{"timestamp":1787696844,"result":"error","metadata":{"uploader":"crier"}}`, wantResult: "ERROR"},
		{name: "mixed case", body: `{"timestamp":1787696844,"result":"FaIlUrE"}`, wantResult: "FAILURE"},
		{name: "absent", status: 404, problem: "absent"},
		{name: "malformed", body: `{`, problem: "malformed"},
		{name: "empty", body: `{}`, problem: "malformed"},
		{name: "null", body: `null`, problem: "malformed"},
		{name: "missing timestamp", body: `{"result":"SUCCESS"}`, problem: "malformed"},
		{name: "zero timestamp", body: `{"timestamp":0,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "negative timestamp", body: `{"timestamp":-1,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "null timestamp", body: `{"timestamp":null,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "string timestamp", body: `{"timestamp":"1787696844","result":"SUCCESS"}`, problem: "malformed"},
		{name: "fractional timestamp", body: `{"timestamp":1787696844.5,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "milliseconds timestamp", body: `{"timestamp":1787696844000,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "overflow timestamp", body: `{"timestamp":9223372036854775807,"result":"SUCCESS"}`, problem: "malformed"},
		{name: "missing result", body: `{"timestamp":1787696844,"passed":true}`, problem: "malformed"},
		{name: "null result", body: `{"timestamp":1787696844,"result":null}`, problem: "malformed"},
		{name: "unknown result", body: `{"timestamp":1787696844,"result":"unknown"}`, problem: "malformed"},
		{name: "running result", body: `{"timestamp":1787696844,"result":"RUNNING"}`, problem: "malformed"},
		{name: "lowercase pending", body: `{"timestamp":1787696844,"result":"pending"}`, problem: "malformed"},
		{name: "padded result", body: `{"timestamp":1787696844,"result":" SUCCESS"}`, problem: "malformed"},
		{name: "network", status: -1, wantErr: true},
		{name: "body interrupted", status: -2, wantErr: true},
		{name: "throttled", status: 429, wantErr: true},
		{name: "server error", status: 503, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var problems, logs []string
			logger := funcr.New(func(_, message string) { logs = append(logs, message) }, funcr.Options{})
			ctx := utils.ContextWithLogger(t.Context(), logger)
			ctx = snapshot.WithProwArtifactProblemHandler(ctx, func(source, reason string) { problems = append(problems, source+"/"+reason) })
			path := artifactTestPrefix + "/finished.json"
			client := artifactClient(t, map[string]string{path: tc.body}, map[string]int{path: tc.status})
			got, err := fetchProwCompletion(ctx, client, "gs://test-platform-results/"+artifactTestPrefix)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantResult == "" {
				if got != nil {
					t.Fatalf("invalid or absent completion must remain pending: %+v", got)
				}
			} else if got == nil || got.Result != tc.wantResult || got.FinishedAt != time.Unix(1787696844, 0).UTC() {
				t.Fatalf("completion = %+v, want native result %s and timestamp", got, tc.wantResult)
			}
			if got != nil {
				info, err := snapshot.ParseProwURL(artifactTestURL)
				if err != nil {
					t.Fatal(err)
				}
				outcome := outcomeForProw(info, *got, runDetail{})
				if outcome.OverallResult != tc.wantResult || outcome.Failed != (tc.wantResult != "SUCCESS") {
					t.Fatalf("incorrect normalized outcome: %+v", outcome)
				}
			}
			if tc.problem != "" {
				if len(problems) != 1 || problems[0] != "job/"+tc.problem || len(logs) == 0 {
					t.Fatalf("missing permanent source diagnostic: problems=%v logs=%v", problems, logs)
				}
			} else if len(problems) != 0 {
				t.Fatalf("unexpected permanent problem: %v", problems)
			}
		})
	}
}

func TestFetchProwCompletionRootAuthority(t *testing.T) {
	for _, bucket := range []string{"test-platform-results", "test-platform-results-public"} {
		for _, prefix := range []string{
			"logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/123",
			"pr-logs/pull/Azure_ARO-HCP/1/pull-ci-Azure-ARO-HCP-main-e2e-parallel/123",
			"pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/123",
		} {
			for _, scheme := range []string{"gs://", "https://prow.ci.openshift.org/view/gs/"} {
				t.Run(scheme+bucket+"/"+prefix, func(t *testing.T) {
					requests := 0
					client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
						requests++
						wantURL := "https://storage.googleapis.com/" + bucket + "/" + prefix + "/finished.json"
						if r.Method != http.MethodGet || r.URL.String() != wantURL {
							t.Fatalf("non-root completion request: %s %s", r.Method, r.URL)
						}
						return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
					})}
					got, err := fetchProwCompletion(t.Context(), client, scheme+bucket+"/"+prefix)
					if got != nil || err != nil || requests != 1 {
						t.Fatalf("root absence must remain pending without fallback: completion=%+v err=%v requests=%d", got, err, requests)
					}
				})
			}
		}
	}
}

func TestFetchProwCompletionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	_, err := fetchProwCompletion(ctx, client, artifactTestURL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestFetchProwRunStartTime(t *testing.T) {
	const start = "2026-08-25T09:47:22+01:00"
	want, err := time.Parse(time.RFC3339, start)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, metadata, started          string
		metadataStatus, startedStatus    int
		wantStart                        time.Time
		wantErr, wantADO, wantDiagnostic bool
	}{
		{name: "prefer metadata", metadata: `{"status":{"startTime":"` + start + `"}}`, wantStart: want, wantADO: true},
		{name: "fallback", metadata: `{}`, started: `{"timestamp":1787696844}`, wantStart: time.Unix(1787696844, 0), wantADO: true, wantDiagnostic: true},
		{name: "malformed start", metadata: `{"status":{"startTime":"yesterday"}}`, started: `{"timestamp":1787696844}`, wantStart: time.Unix(1787696844, 0), wantADO: true, wantDiagnostic: true},
		{name: "numeric start", metadata: `{"status":{"startTime":123}}`, started: `{"timestamp":1787696844}`, wantStart: time.Unix(1787696844, 0), wantADO: true, wantDiagnostic: true},
		{name: "null start", metadata: `{"status":{"startTime":null}}`, startedStatus: 404, wantADO: true, wantDiagnostic: true},
		{name: "zero start", metadata: `{"status":{"startTime":"0001-01-01T00:00:00Z"}}`, startedStatus: 404, wantADO: true, wantDiagnostic: true},
		{name: "absent metadata", metadataStatus: 404, started: `{"timestamp":1787696844}`, wantStart: time.Unix(1787696844, 0), wantDiagnostic: true},
		{name: "malformed metadata", metadata: `{`, started: `{"timestamp":1787696844}`, wantStart: time.Unix(1787696844, 0), wantDiagnostic: true},
		{name: "metadata throttled", metadataStatus: 429, wantErr: true},
		{name: "metadata server error", metadataStatus: 503, wantErr: true},
		{name: "metadata network", metadataStatus: -1, wantErr: true},
		{name: "metadata interrupted", metadataStatus: -2, wantErr: true},
		{name: "both absent", metadataStatus: 404, startedStatus: 404, wantDiagnostic: true},
		{name: "started malformed", metadata: `{}`, started: `{`, wantADO: true, wantDiagnostic: true},
		{name: "started null", metadata: `{}`, started: `null`, wantADO: true, wantDiagnostic: true},
		{name: "started missing timestamp", metadata: `{}`, started: `{}`, wantADO: true, wantDiagnostic: true},
		{name: "started zero timestamp", metadata: `{}`, started: `{"timestamp":0}`, wantADO: true, wantDiagnostic: true},
		{name: "started negative timestamp", metadata: `{}`, started: `{"timestamp":-1}`, wantADO: true, wantDiagnostic: true},
		{name: "started milliseconds timestamp", metadata: `{}`, started: `{"timestamp":1787696844000}`, wantADO: true, wantDiagnostic: true},
		{name: "started throttled", metadata: `{}`, startedStatus: 429, wantErr: true, wantADO: true, wantDiagnostic: true},
		{name: "started server error", metadata: `{}`, startedStatus: 503, wantErr: true, wantADO: true, wantDiagnostic: true},
		{name: "started network", metadata: `{}`, startedStatus: -1, wantErr: true, wantADO: true, wantDiagnostic: true},
		{name: "started interrupted", metadata: `{}`, startedStatus: -2, wantErr: true, wantADO: true, wantDiagnostic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := tc.metadata
			if tc.wantADO {
				metadata = `{"metadata":{"annotations":{"ev2.rollout/build":"00123"}},` + strings.TrimPrefix(metadata, "{")
				metadata = strings.ReplaceAll(metadata, ",}", "}")
			}
			objects := map[string]string{artifactTestPrefix + "/prowjob.json": metadata}
			if tc.started != "" {
				objects[artifactTestPrefix+"/started.json"] = tc.started
			}
			statuses := map[string]int{artifactTestPrefix + "/prowjob.json": tc.metadataStatus, artifactTestPrefix + "/started.json": tc.startedStatus}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
			got, err := fetchJobOutcomeDetail(ctx, artifactClient(t, objects, statuses), artifactTestURL)
			if (err != nil) != tc.wantErr || !got.StartedAt.Equal(tc.wantStart) {
				t.Fatalf("detail=%+v err=%v, want start=%v err=%v", got, err, tc.wantStart, tc.wantErr)
			}
			if tc.wantADO && got.ADOBuildID != "00123" {
				t.Fatalf("lost annotation with missing or malformed start: %+v", got)
			}
			if (len(problems) > 0) != tc.wantDiagnostic {
				t.Fatalf("problems=%v, want diagnostic=%v", problems, tc.wantDiagnostic)
			}
			if !got.FinishedAt.IsZero() {
				t.Fatalf("enrichment must not supply completion: %+v", got)
			}
		})
	}
}

func TestFetchProwCompletionInvalidURI(t *testing.T) {
	client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected request: %s", r.URL)
	})}
	if got, err := fetchProwCompletion(t.Context(), client, "gs://bucket/not-a-job"); got != nil || err == nil {
		t.Fatalf("invalid URI: completion=%+v err=%v", got, err)
	}
}
