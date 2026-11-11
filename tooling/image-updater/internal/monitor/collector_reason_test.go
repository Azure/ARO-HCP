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
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeReasonClient struct {
	reasons map[string]string
	calls   map[string]int
}

func (f *fakeReasonClient) FailureReason(_ context.Context, _, buildID string) string {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[buildID]++
	return f.reasons[buildID]
}

func newReasonTestCollector(prow ProwClient, reasons ReasonClient) *Collector {
	logger := slog.New(slog.DiscardHandler)
	return NewCollector(prow, regexp.MustCompile(`^periodic-ci-Azure-ARO-HCP-main-image-updater-`), logger, reasons)
}

func TestCollectorConsecutiveFailuresCarriesFailureReason(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "success", "gs://b/1", now.Add(-4*time.Hour), now.Add(-3*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "failure", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}
	reasons := &fakeReasonClient{reasons: map[string]string{
		"2": "Run multi-stage step aro-hcp-automation-image-update: ERROR:command failed{\"err\": \"no creation timestamp\"}",
	}}

	collector := newReasonTestCollector(client, reasons)
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatalf("register collector: %v", err)
	}

	expected := `
# HELP image_updater_consecutive_failures Number of consecutive failed or errored completed runs since the most recent success; build_id, failure_reason and url labels identify the most recent failing run and its classified failure reason
# TYPE image_updater_consecutive_failures gauge
image_updater_consecutive_failures{build_id="2",failure_reason="Run multi-stage step aro-hcp-automation-image-update: ERROR:command failed{\"err\": \"no creation timestamp\"}",job_name="periodic-ci-Azure-ARO-HCP-main-image-updater-tooling",url="gs://b/2"} 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected), metricConsecutive); err != nil {
		t.Fatalf("metrics comparison failed: %v", err)
	}
}

func TestCollectorFailureReasonCachedPerBuild(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "failure", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}
	reasons := &fakeReasonClient{reasons: map[string]string{"2": "boom"}}

	collector := newReasonTestCollector(client, reasons)
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())
	collector.poll(context.Background())

	if got := reasons.calls["2"]; got != 1 {
		t.Fatalf("FailureReason called %d times for build 2, want 1 (cached after first poll)", got)
	}
}

func TestCollectorEmptyFailureReasonWhenClassificationUnavailable(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "failure", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}

	// Reason client returns nothing (e.g. 404 on the junit artifact).
	collector := newReasonTestCollector(client, &fakeReasonClient{})
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	snapshot := collector.snapshots["periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"]
	if snapshot.latestFailure == nil {
		t.Fatal("latest failure missing")
	}
	if got := collector.failureReason(context.Background(), "job", "2"); got != "" {
		// sanity: uncached lookup of an unknown build stays empty
		t.Fatalf("unexpected reason %q", got)
	}
	if snapshot.failureReason != "" {
		t.Fatalf("snapshot failure reason = %q, want empty", snapshot.failureReason)
	}
}

func TestCollectorNoReasonFetchWithoutFailures(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "success", "gs://b/1", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}
	reasons := &fakeReasonClient{reasons: map[string]string{"1": "should not be fetched"}}

	collector := newReasonTestCollector(client, reasons)
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	if len(reasons.calls) != 0 {
		t.Fatalf("FailureReason called for successful run: %v", reasons.calls)
	}
}
