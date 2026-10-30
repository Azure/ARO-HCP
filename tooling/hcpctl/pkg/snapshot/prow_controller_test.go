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
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/util/timing"
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
	for _, source := range []string{"e2e", "timing", "config", "observability", "job", "slot-state"} {
		for _, reason := range []string{"absent", "malformed", "unsupported-version"} {
			ReportProwArtifactProblem(ctx, source, reason)
		}
	}
	ReportProwArtifactProblem(ctx, "arbitrary-job-name", "absent")
	ReportProwArtifactProblem(ctx, "job", "arbitrary-error")
	if len(problems) != 18 {
		t.Fatalf("unexpected categories: %v", problems)
	}
	ReportProwArtifactProblem(t.Context(), "job", "absent")
	ReportProwArtifactProblem(WithProwArtifactProblemHandler(t.Context(), nil), "job", "absent")
}

func TestFetchTestTimingsCleanupBoundary(t *testing.T) {
	t.Parallel()
	const prefix = "logs/job/123/" + timingMetadataPath + "timing-metadata-"
	for _, fullLifecycle := range []bool{false, true} {
		name := "historical"
		if fullLifecycle {
			name = "full-lifecycle"
		}
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: controllerArtifactTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				if r.URL.Query().Get("prefix") == prefix {
					body = `{"items":[{"name":"` + prefix + `test.yaml"}]}`
				} else {
					body = "identifier: [test]\nfinishedAt: '2026-10-30T14:39:42.324789Z'\n"
					if fullLifecycle {
						body += "cleanupStartedAt: '2026-10-30T14:20:23.155483Z'\n"
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			gcs, err := controllerGCSClient(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer gcs.Close()
			got, err := fetchTestTimings(t.Context(), gcs, "bucket", "logs/job/123", logr.Discard(), client)
			if err != nil {
				t.Fatal(err)
			}
			want := "2026-10-30T14:39:42.324789Z"
			if fullLifecycle {
				want = "2026-10-30T14:20:23.155483Z"
			}
			boundary, _ := time.Parse(time.RFC3339Nano, want)
			if !got["test"].CleanupStartTime.Equal(boundary) {
				t.Fatalf("wrong cleanup start: got %s, want %s", got["test"].CleanupStartTime, boundary)
			}
		})
	}
}

func TestFetchTestTimingsPreCleanupBoundaries(t *testing.T) {
	t.Parallel()
	const prefix = "logs/job/123/" + timingMetadataPath + "timing-metadata-"
	start, err := time.Parse(time.RFC3339Nano, "2026-10-30T10:00:00.123456789Z")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := start.Add(10 * time.Minute)
	step := func(name string, from, to time.Duration) timing.StepTimingMetadata {
		return timing.StepTimingMetadata{Name: name, StartedAt: start.Add(from).Format(time.RFC3339Nano), FinishedAt: start.Add(to).Format(time.RFC3339Nano)}
	}
	for _, tc := range []struct {
		name           string
		steps          []timing.StepTimingMetadata
		setupFinish    time.Time
		testStart      time.Time
		hasBodyRequest bool
	}{
		{name: "no spans", testStart: start, hasBodyRequest: true},
		{
			name: "cleanup only",
			steps: []timing.StepTimingMetadata{
				step("Delete resource group", 10*time.Minute, 11*time.Minute),
				step("Release identity container", 11*time.Minute, 12*time.Minute),
			},
			testStart: start, hasBodyRequest: true,
		},
		{
			name: "setup and cleanup only",
			steps: []timing.StepTimingMetadata{
				step("Lease identity container", 0, 2*time.Minute),
				step("Delete resource group", 10*time.Minute, 11*time.Minute),
				step("Release identity container", 11*time.Minute, 12*time.Minute),
			},
			setupFinish: start.Add(2 * time.Minute), testStart: start.Add(2 * time.Minute), hasBodyRequest: true,
		},
		{
			name: "body span crosses cleanup",
			steps: []timing.StepTimingMetadata{
				step("Lease identity container", 0, 2*time.Minute),
				step("Create cluster", 3*time.Minute, 11*time.Minute),
				step("Delete resource group", 10*time.Minute, 12*time.Minute),
			},
			setupFinish: start.Add(2 * time.Minute), testStart: start.Add(3 * time.Minute), hasBodyRequest: true,
		},
		{
			name: "setup span crosses cleanup",
			steps: []timing.StepTimingMetadata{
				step("Lease identity container", 0, 11*time.Minute),
				step("Delete resource group", 10*time.Minute, 12*time.Minute),
			},
			setupFinish: cleanup, testStart: cleanup,
		},
		{
			name: "setup overlaps body",
			steps: []timing.StepTimingMetadata{
				step("Lease identity container", 0, 5*time.Minute),
				step("Create cluster", 3*time.Minute, 9*time.Minute),
			},
			setupFinish: start.Add(3 * time.Minute), testStart: start.Add(3 * time.Minute), hasBodyRequest: true,
		},
	} {
		for _, fullLifecycle := range []bool{false, true} {
			mode := "legacy"
			if fullLifecycle {
				mode = "full lifecycle"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				metadata := timing.SpecTimingMetadata{
					Identifier: []string{"test"},
					StartedAt:  start.Format(time.RFC3339Nano),
					FinishedAt: cleanup.Format(time.RFC3339Nano),
					Steps:      tc.steps,
				}
				finish := cleanup
				if fullLifecycle {
					metadata.CleanupStartedAt = cleanup.Format(time.RFC3339Nano)
					finish = cleanup.Add(2 * time.Minute)
					metadata.FinishedAt = finish.Format(time.RFC3339Nano)
				}
				data, err := yaml.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				client := &http.Client{Transport: controllerArtifactTransport(func(r *http.Request) (*http.Response, error) {
					body := string(data)
					if r.URL.Query().Get("prefix") == prefix {
						body = `{"items":[{"name":"` + prefix + `test.yaml"}]}`
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})}
				gcs, err := controllerGCSClient(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				defer gcs.Close()
				got, err := fetchTestTimings(t.Context(), gcs, "bucket", "logs/job/123", logr.Discard(), client)
				if err != nil {
					t.Fatal(err)
				}
				boundaries := got["test"]
				if !boundaries.SetupFinishTime.Equal(tc.setupFinish) || !boundaries.TestStartTime.Equal(tc.testStart) || !boundaries.CleanupStartTime.Equal(cleanup) {
					t.Fatalf("unexpected phase boundaries: %+v", boundaries)
				}
				if boundaries.TestStartTime.Before(start) ||
					(!boundaries.SetupFinishTime.IsZero() && (boundaries.SetupFinishTime.Before(start) || boundaries.SetupFinishTime.After(boundaries.TestStartTime))) {
					t.Fatalf("setup/test boundaries must stay ordered: %+v", boundaries)
				}
				phases := computePhases(GatherInput{
					TimeWindow:       TimeWindow{Start: start, End: finish.Add(5 * time.Minute), SetupFinishTime: boundaries.SetupFinishTime},
					TestStartTime:    boundaries.TestStartTime,
					CleanupStartTime: boundaries.CleanupStartTime,
				})
				if len(phases) != 2 || phases[0].end.After(phases[1].start) {
					t.Fatalf("expected ordered test and cleanup phases: %+v", phases)
				}
				for _, phase := range phases {
					if phase.start.After(phase.end) {
						t.Fatalf("phase has negative duration: %+v", phase)
					}
				}
				gatherer := &Gatherer{}
				bodyRequest := frontendRequest{Timestamp: start.Add(4 * time.Minute)}
				if tc.hasBodyRequest && (!gatherer.requestInPhase(bodyRequest, phases[0]) || gatherer.requestInPhase(bodyRequest, phases[1])) {
					t.Fatalf("timestamped body request must remain in test phase: %+v", phases)
				}
				cleanupRequest := frontendRequest{Timestamp: cleanup}
				if gatherer.requestInPhase(cleanupRequest, phases[0]) || !gatherer.requestInPhase(cleanupRequest, phases[1]) {
					t.Fatalf("cleanup boundary request must belong only to cleanup phase: %+v", phases)
				}
			})
		}
	}
}
