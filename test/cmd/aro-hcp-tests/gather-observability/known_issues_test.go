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
	"strings"
	"testing"
)

func TestParseKnownIssues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		wantErr bool
		wantLen int
	}{
		{
			name: "valid config",
			content: `knownIssues:
- name: "BackendOperationErrorRate"
  reason: "Known during provisioning"
- name: "BackendController.*"
  reason: "Controller churn known"
`,
			wantLen: 2,
		},
		{
			name:    "empty list",
			content: "knownIssues: []\n",
			wantLen: 0,
		},
		{
			name:    "missing name",
			content: "knownIssues:\n- reason: \"some reason\"\n",
			wantErr: true,
		},
		{
			name:    "missing reason",
			content: "knownIssues:\n- name: \"SomeAlert\"\n",
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			content: "not: [valid: yaml",
			wantErr: true,
		},
		{
			name:    "invalid name regex",
			content: "knownIssues:\n- name: \"[invalid\"\n  reason: \"bad regex\"\n",
			wantErr: true,
		},
		{
			name: "invalid label regex",
			content: `knownIssues:
- name: "SomeAlert"
  reason: "test"
  labels:
    name: "[invalid"
`,
			wantErr: true,
		},
		{
			name: "with labels",
			content: `knownIssues:
- name: "BackendControllerRetryHotLoop"
  reason: "Known for delete controllers"
  labels:
    name: "operation.*delete"
`,
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseKnownIssues([]byte(tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != tt.wantLen {
				t.Errorf("got %d known issues, want %d", len(result), tt.wantLen)
			}
		})
	}
}

func TestDefaultKnownIssuesParse(t *testing.T) {
	t.Parallel()
	issues, err := parseKnownIssues(defaultKnownIssuesData)
	if err != nil {
		t.Fatalf("embedded knownIssues.yaml failed to parse: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("embedded knownIssues.yaml parsed to zero patterns")
	}
}

// TestDefaultKnownIssuesClassification classifies alerts against the actual
// embedded knownIssues.yaml (not an inline copy), so a typo or label mismatch
// in the production entries fails this test directly. Covers the
// FrontendPathMedianLatency PUT/GET split added for AROSLSRE-2232.
func TestDefaultKnownIssuesClassification(t *testing.T) {
	t.Parallel()
	issues, err := parseKnownIssues(defaultKnownIssuesData)
	if err != nil {
		t.Fatalf("embedded knownIssues.yaml failed to parse: %v", err)
	}

	tests := []struct {
		name          string
		alertName     string
		labels        map[string]string
		wantKnown     bool
		wantReasonSub string
	}{
		{
			name:          "median_latency_put_is_known",
			alertName:     "FrontendPathMedianLatency",
			labels:        map[string]string{"method": "PUT", "route": "/clusters"},
			wantKnown:     true,
			wantReasonSub: "AROSLSRE-2232",
		},
		{
			name:          "median_latency_lowercase_put_is_known",
			alertName:     "FrontendPathMedianLatency",
			labels:        map[string]string{"method": "put", "route": "/clusters"},
			wantKnown:     true,
			wantReasonSub: "AROSLSRE-2232",
		},
		{
			name:      "median_latency_get_is_not_known",
			alertName: "FrontendPathMedianLatency",
			labels:    map[string]string{"method": "GET", "route": "/clusters"},
			wantKnown: false,
		},
		{
			name:      "median_latency_head_is_not_known",
			alertName: "FrontendPathMedianLatency",
			labels:    map[string]string{"method": "HEAD", "route": "/clusters"},
			wantKnown: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			classified := classifyAlerts([]alert{{Alert: alertData{Name: tt.alertName, Labels: tt.labels}}}, issues)
			if len(classified) != 1 {
				t.Fatalf("expected 1 classified alert, got %d", len(classified))
			}
			got := classified[0].Metadata.KnownIssue
			if got != tt.wantKnown {
				t.Errorf("%s{%v}: KnownIssue = %v, want %v (reason: %q)", tt.alertName, tt.labels, got, tt.wantKnown, classified[0].Metadata.KnownIssueReason)
			}
			if tt.wantReasonSub != "" && !strings.Contains(classified[0].Metadata.KnownIssueReason, tt.wantReasonSub) {
				t.Errorf("%s{%v}: KnownIssueReason = %q, want substring %q", tt.alertName, tt.labels, classified[0].Metadata.KnownIssueReason, tt.wantReasonSub)
			}
		})
	}
}

func TestClassifyAlerts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		knownIssuesYAML string
		alerts          []alert
	}{
		{
			name: "basic",
			knownIssuesYAML: `knownIssues:
- name: "BackendOperationErrorRate"
  reason: "error rate known"
- name: "BackendController.*"
  reason: "controller churn"
`,
			alerts: []alert{
				{Alert: alertData{Name: "BackendOperationErrorRate"}},
				{Alert: alertData{Name: "BackendControllerRetryHotLoop"}},
				{Alert: alertData{Name: "BackendControllerQueueDepthHigh"}},
				{Alert: alertData{Name: "SomethingUnknown"}},
				{Alert: alertData{Name: "AnotherUnknown"}},
			},
		},
		{
			name: "no_known_issues",
			alerts: []alert{
				{Alert: alertData{Name: "SomeAlert"}},
			},
		},
		{
			name: "exact_match_only",
			knownIssuesYAML: `knownIssues:
- name: "Backend"
  reason: "exact match only"
`,
			alerts: []alert{
				{Alert: alertData{Name: "Backend"}},
				{Alert: alertData{Name: "BackendOperationErrorRate"}},
			},
		},
		{
			name: "first_match_wins",
			knownIssuesYAML: `knownIssues:
- name: "Backend.*"
  reason: "first pattern"
- name: "BackendControllerRetryHotLoop"
  reason: "second pattern"
`,
			alerts: []alert{
				{Alert: alertData{Name: "BackendControllerRetryHotLoop"}},
			},
		},
		{
			name: "label_matching",
			knownIssuesYAML: `knownIssues:
- name: "BackendControllerRetryHotLoop"
  reason: "known for delete controller"
  labels:
    name: "operationnodepooldelete"
`,
			alerts: []alert{
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationnodepooldelete", "severity": "warning"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationcreate", "severity": "warning"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: nil,
				}},
			},
		},
		{
			name: "label_regex",
			knownIssuesYAML: `knownIssues:
- name: "BackendControllerRetryHotLoop"
  reason: "known for delete controllers"
  labels:
    name: "operation.*delete"
`,
			alerts: []alert{
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationnodepooldelete"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationclusterdelete"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationcreate"},
				}},
			},
		},
		{
			name: "median_latency_mutating_methods",
			knownIssuesYAML: `knownIssues:
- name: "FrontendPathMedianLatency"
  reason: "read-path SLO"
  labels:
    method: "(?i)(PUT|POST|PATCH|DELETE)"
`,
			alerts: []alert{
				{Alert: alertData{
					Name:   "FrontendPathMedianLatency",
					Labels: map[string]string{"method": "PUT", "route": "/clusters"},
				}},
				{Alert: alertData{
					Name:   "FrontendPathMedianLatency",
					Labels: map[string]string{"method": "put", "route": "/clusters"},
				}},
				{Alert: alertData{
					Name:   "FrontendPathMedianLatency",
					Labels: map[string]string{"method": "GET", "route": "/clusters"},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var issues []knownIssue
			if tt.knownIssuesYAML != "" {
				issues = mustParse(t, tt.knownIssuesYAML)
			}
			classified := classifyAlerts(tt.alerts, issues)
			CompareWithFixture(t, classified)
		})
	}
}
