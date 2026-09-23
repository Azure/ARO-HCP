// Copyright 2025 Microsoft Corporation
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

package gatherobservability

import (
	"encoding/xml"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func mustTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

var defaultTimeWindow = timing.TimeWindow{
	Start: *mustTime("2026-04-13T06:00:00Z"),
	End:   *mustTime("2026-04-13T08:00:00Z"),
}

func TestBuildTestName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workspace string
		ruleName  string
		want      string
	}{
		{
			name:      "svc_workspace",
			workspace: "svc",
			ruleName:  "MiseEnvoyScrapeDown",
			want:      "[aro-hcp-observability] [svc] alert MiseEnvoyScrapeDown does not fire",
		},
		{
			name:      "hcp_workspace",
			workspace: "hcp",
			ruleName:  "BackendControllerRetryHotLoop",
			want:      "[aro-hcp-observability] [hcp] alert BackendControllerRetryHotLoop does not fire",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildTestName(tt.workspace, tt.ruleName)
			if got != tt.want {
				t.Errorf("buildTestName() =\n  %q\nwant:\n  %q", got, tt.want)
			}
		})
	}
}

func TestMergeIntervals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		intervals []timeInterval
		want      []timeInterval
	}{
		{
			name:      "empty",
			intervals: nil,
			want:      nil,
		},
		{
			name:      "single",
			intervals: []timeInterval{{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:10:00Z")}},
			want:      []timeInterval{{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:10:00Z")}},
		},
		{
			name: "unsorted_overlapping",
			intervals: []timeInterval{
				{start: *mustTime("2026-04-13T06:15:00Z"), end: *mustTime("2026-04-13T06:35:00Z")},
				{start: *mustTime("2026-04-13T06:10:00Z"), end: *mustTime("2026-04-13T06:30:00Z")},
			},
			want: []timeInterval{
				{start: *mustTime("2026-04-13T06:10:00Z"), end: *mustTime("2026-04-13T06:35:00Z")},
			},
		},
		{
			name: "full_containment",
			intervals: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T07:00:00Z")},
				{start: *mustTime("2026-04-13T06:10:00Z"), end: *mustTime("2026-04-13T06:20:00Z")},
			},
			want: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T07:00:00Z")},
			},
		},
		{
			name: "touching_boundaries",
			intervals: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:10:00Z")},
				{start: *mustTime("2026-04-13T06:10:00Z"), end: *mustTime("2026-04-13T06:20:00Z")},
			},
			want: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:20:00Z")},
			},
		},
		{
			name: "disjoint",
			intervals: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:10:00Z")},
				{start: *mustTime("2026-04-13T07:00:00Z"), end: *mustTime("2026-04-13T07:10:00Z")},
			},
			want: []timeInterval{
				{start: *mustTime("2026-04-13T06:00:00Z"), end: *mustTime("2026-04-13T06:10:00Z")},
				{start: *mustTime("2026-04-13T07:00:00Z"), end: *mustTime("2026-04-13T07:10:00Z")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mergeIntervals(tt.intervals)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeIntervals() returned %d intervals, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !got[i].start.Equal(tt.want[i].start) || !got[i].end.Equal(tt.want[i].end) {
					t.Errorf("interval[%d] = {%v, %v}, want {%v, %v}",
						i, got[i].start, got[i].end, tt.want[i].start, tt.want[i].end)
				}
			}
		})
	}
}

func TestComputeGroupDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		firings []alert
		want    float64
	}{
		{
			name:    "nil_starts_at_skipped",
			firings: []alert{{Alert: alertData{StartsAt: nil}}},
			want:    0,
		},
		{
			name: "ends_before_starts_clamped_to_zero",
			firings: []alert{{Alert: alertData{
				StartsAt: mustTime("2026-04-13T07:30:00Z"),
				EndsAt:   mustTime("2026-04-13T07:00:00Z"),
			}}},
			want: 0,
		},
		{
			name: "uses_window_end_when_no_ends_at",
			firings: []alert{{Alert: alertData{
				StartsAt: mustTime("2026-04-13T07:00:00Z"),
			}}},
			want: 3600,
		},
		{
			name: "overlapping_firings",
			firings: []alert{
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:10:00Z"),
					EndsAt:   mustTime("2026-04-13T06:30:00Z"),
				}},
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:15:00Z"),
					EndsAt:   mustTime("2026-04-13T06:35:00Z"),
				}},
			},
			want: 1500, // merged 06:10–06:35 = 25min, not 20+20=40min
		},
		{
			name: "concurrent_firings",
			firings: []alert{
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:00:00Z"),
					EndsAt:   mustTime("2026-04-13T06:20:00Z"),
				}},
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:00:00Z"),
					EndsAt:   mustTime("2026-04-13T06:22:00Z"),
				}},
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:01:00Z"),
					EndsAt:   mustTime("2026-04-13T06:21:00Z"),
				}},
			},
			want: 1320, // merged 06:00–06:22 = 22min, not 20+22+20=62min
		},
		{
			name: "touching_firings",
			firings: []alert{
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:00:00Z"),
					EndsAt:   mustTime("2026-04-13T06:10:00Z"),
				}},
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:10:00Z"),
					EndsAt:   mustTime("2026-04-13T06:20:00Z"),
				}},
			},
			want: 1200, // merged 06:00–06:20 = 20min (touching boundary merges)
		},
		{
			name: "non_overlapping_firings",
			firings: []alert{
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T06:00:00Z"),
					EndsAt:   mustTime("2026-04-13T06:10:00Z"),
				}},
				{Alert: alertData{
					StartsAt: mustTime("2026-04-13T07:00:00Z"),
					EndsAt:   mustTime("2026-04-13T07:10:00Z"),
				}},
			},
			want: 1200, // 10min + 10min = 20min (no overlap)
		},
		{
			name:    "empty_firings",
			firings: nil,
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeGroupDuration(tt.firings, defaultTimeWindow)
			if got != tt.want {
				t.Errorf("computeGroupDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAlertsToJUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		workspaces map[string]*workspaceData
	}{
		{
			name: "main",
			workspaces: map[string]*workspaceData{
				"svc": {
					Type: "svc",
					AlertRules: []string{
						"BackendControllerRetryHotLoop",
						"KubePodImagePull",
						"MiseEnvoyScrapeDown",
						"SomeRuleThatDidNotFire",
					},
					FiredAlerts: []alert{
						{
							Alert: alertData{
								Name: "BackendControllerRetryHotLoop", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
								StartsAt: mustTime("2026-04-13T06:10:00Z"), EndsAt: mustTime("2026-04-13T06:53:23Z"),
								Labels:      map[string]string{"alertname": "BackendControllerRetryHotLoop", "name": "operationnodepoolcreate", "severity": "warning", "cluster": "prow-j3151872-svc", "namespace": "aro-hcp"},
								Description: "Backend controller workqueue operationnodepoolcreate has a retry ratio of > 50%",
							},
							Metadata: alertMetadata{KnownIssue: true, KnownIssueReason: "Nodepool create controller retry hot loops are observed during e2e runs. Needs investigation."},
						},
						{
							Alert: alertData{
								Name: "MiseEnvoyScrapeDown", Severity: armalertsmanagement.SeveritySev3, Condition: "Resolved",
								StartsAt: mustTime("2026-04-13T06:20:00Z"), EndsAt: mustTime("2026-04-13T06:30:00Z"),
								Labels:      map[string]string{"alertname": "MiseEnvoyScrapeDown", "severity": "warning", "cluster": "prow-j3151872-svc", "namespace": "aro-hcp"},
								Description: "Mise Envoy scrape target is down",
							},
							Metadata: alertMetadata{KnownIssue: true, KnownIssueReason: "Mise Envoy scrape targets intermittently go down during e2e runs."},
						},
						{
							Alert: alertData{
								Name: "KubePodImagePull", Severity: armalertsmanagement.SeveritySev4, Condition: "Fired",
								StartsAt: mustTime("2026-04-13T07:00:00Z"),
								Labels:   map[string]string{"alertname": "KubePodImagePull", "severity": "warning", "cluster": "prow-j3151872-svc", "pod": "frontend-abc123", "namespace": "aro-hcp"},
							},
						},
					},
				},
				"hcp": {
					Type:       "hcp",
					AlertRules: []string{"HCPClusterHealth"},
				},
			},
		},
		{
			name: "same_alert_rule_multiple_workspaces",
			workspaces: map[string]*workspaceData{
				"svc": {Type: "svc", AlertRules: []string{"KubeNodeNotReady"}},
				"hcp": {
					Type:       "hcp",
					AlertRules: []string{"KubeNodeNotReady"},
					FiredAlerts: []alert{{
						Alert: alertData{
							Name: "KubeNodeNotReady", Severity: armalertsmanagement.SeveritySev2, Condition: "Fired",
							StartsAt: mustTime("2026-04-13T07:00:00Z"),
							Labels:   map[string]string{"alertname": "KubeNodeNotReady", "node": "worker-1"},
						},
					}},
				},
			},
		},
		{
			name: "no_alert_rules",
			workspaces: map[string]*workspaceData{
				"svc": {
					Type: "svc",
					FiredAlerts: []alert{{
						Alert: alertData{
							Name: "UnexpectedAlert", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
							StartsAt: mustTime("2026-04-13T07:00:00Z"),
							Labels:   map[string]string{"alertname": "UnexpectedAlert"},
						},
					}},
				},
			},
		},
		{
			name: "alert_not_in_rules",
			workspaces: map[string]*workspaceData{
				"svc": {
					Type:       "svc",
					AlertRules: []string{"DefinedRule"},
					FiredAlerts: []alert{{
						Alert: alertData{
							Name: "UndefinedAlert", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
							StartsAt: mustTime("2026-04-13T07:00:00Z"),
							Labels:   map[string]string{"alertname": "UndefinedAlert"},
						},
					}},
				},
			},
		},
		{
			name: "known_and_unknown_firings_in_alert_rule",
			workspaces: map[string]*workspaceData{
				"svc": {
					Type:       "svc",
					AlertRules: []string{"BackendControllerRetryHotLoop"},
					FiredAlerts: []alert{
						{
							Alert: alertData{
								Name: "BackendControllerRetryHotLoop", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
								StartsAt: mustTime("2026-04-13T06:10:00Z"), EndsAt: mustTime("2026-04-13T06:30:00Z"),
								Labels:      map[string]string{"alertname": "BackendControllerRetryHotLoop", "name": "operationnodepoolcreate", "severity": "warning", "cluster": "prow-j3151872-svc"},
								Description: "Backend controller retry hot loop (known firing)",
							},
							Metadata: alertMetadata{KnownIssue: true, KnownIssueReason: "Known issue: hot loop during provisioning."},
						},
						{
							Alert: alertData{
								Name: "BackendControllerRetryHotLoop", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
								StartsAt: mustTime("2026-04-13T07:00:00Z"), EndsAt: mustTime("2026-04-13T07:15:00Z"),
								Labels:      map[string]string{"alertname": "BackendControllerRetryHotLoop", "name": "operationnodepoolcreate", "severity": "warning", "cluster": "prow-j3151872-svc"},
								Description: "Backend controller retry hot loop (unknown firing)",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			suites := alertsToJUnit(logr.Discard(), tt.workspaces, defaultTimeWindow)
			xmlBytes, err := xml.MarshalIndent(suites, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal JUnit XML: %v", err)
			}
			CompareWithFixture(t, string(xmlBytes), WithExtension(".xml"))
		})
	}
}
