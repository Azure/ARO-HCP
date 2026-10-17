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
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/timing"
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
  expiresAfter: "2026-10-31"
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
			name:    "optional expiry",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n",
			wantLen: 1,
		},
		{
			name:    "invalid expiry format",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  expiresAfter: \"October 31, 2026\"\n",
			wantErr: true,
		},
		{
			name:    "invalid expiry date",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  expiresAfter: \"2026-02-30\"\n",
			wantErr: true,
		},
		{
			name:    "misspelled expiry field",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  expiresAfer: \"2026-10-31\"\n",
			wantErr: true,
		},
		{
			name:    "misspelled top-level field",
			content: "knownIssue: []\n",
			wantErr: true,
		},
		{
			name:    "duplicate field",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  reason: \"duplicate\"\n",
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

func TestEmbeddedKnownIssuesParse(t *testing.T) {
	t.Parallel()
	if _, err := parseKnownIssues(defaultKnownIssuesData); err != nil {
		t.Fatalf("embedded known issues must parse: %v", err)
	}
}

func TestUndatedKnownIssueDoesNotExpire(t *testing.T) {
	t.Parallel()
	issues := mustParse(t, `knownIssues:
- name: "SomeAlert"
  reason: "existing exception"
`)
	got := classifyAlertsAt([]alert{{Alert: alertData{Name: "SomeAlert"}}}, issues, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if !got[0].Metadata.KnownIssue || got[0].Metadata.KnownIssueReason != "existing exception" {
		t.Errorf("undated exception should still classify the firing as known: %+v", got[0].Metadata)
	}
}

func TestClassifyAlertsExpiry(t *testing.T) {
	t.Parallel()
	issues := mustParse(t, `knownIssues:
- name: "SomeAlert"
  reason: "temporary exception"
  expiresAfter: "2026-10-31"
- name: "SomeAlert"
  reason: "another active exception"
  labels:
    component: "fallback"
  expiresAfter: "2026-12-31"
`)
	alerts := []alert{
		{Alert: alertData{Name: "SomeAlert", Labels: map[string]string{"component": "fallback"}}},
		{Alert: alertData{Name: "SomeAlert", Labels: map[string]string{"component": "other"}}},
	}
	for _, tt := range []struct {
		name        string
		at          time.Time
		known       []bool
		firstReason string
	}{
		{name: "before expiry", at: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), known: []bool{true, true}, firstReason: "temporary exception"},
		{name: "through expiry date", at: time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC), known: []bool{true, true}, firstReason: "temporary exception"},
		{name: "after expiry", at: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), known: []bool{true, false}, firstReason: "another active exception"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyAlertsAt(alerts, issues, tt.at)
			for i, want := range tt.known {
				if got[i].Metadata.KnownIssue != want {
					t.Errorf("alert %d known=%v, want %v", i, got[i].Metadata.KnownIssue, want)
				}
			}
			if got[0].Metadata.KnownIssueReason != tt.firstReason {
				t.Errorf("first alert reason=%q, want %q", got[0].Metadata.KnownIssueReason, tt.firstReason)
			}
			if got[1].Metadata.KnownIssueReason != "" && !tt.known[1] {
				t.Errorf("expired alert retained known issue reason %q", got[1].Metadata.KnownIssueReason)
			}
		})
	}
}

func TestExpiredKnownIssueFailsAlertJUnit(t *testing.T) {
	t.Parallel()
	issues := mustParse(t, `knownIssues:
- name: "SomeAlert"
  reason: "temporary exception"
  expiresAfter: "2026-10-31"
`)
	for _, tt := range []struct {
		name        string
		at          time.Time
		wantFailed  uint
		wantSkipped uint
	}{
		{name: "before expiry", at: time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC), wantSkipped: 1},
		{name: "after expiry", at: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), wantFailed: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := &workspaceData{
				Type:        workspaceSvc,
				AlertRules:  []string{"SomeAlert"},
				FiredAlerts: classifyAlertsAt([]alert{{Alert: alertData{Name: "SomeAlert"}}}, issues, tt.at),
			}
			suite := workspaceDataToJUnit(logr.Discard(), ws, timing.TimeWindow{})
			if suite.NumFailed != tt.wantFailed || suite.NumSkipped != tt.wantSkipped {
				t.Errorf("JUnit failures=%d skips=%d; want failures=%d skips=%d", suite.NumFailed, suite.NumSkipped, tt.wantFailed, tt.wantSkipped)
			}
		})
	}
}

// TestDefaultKnownIssuesClassification classifies alerts against the actual
// embedded knownIssues.yaml (not an inline copy), so a typo or label mismatch
// in the production entries fails this test directly. Covers the
// FrontendPathMedianLatency PUT/GET split added for AROSLSRE-2232.
//
// Uses a fixed reference time rather than classifyAlerts' wall clock: the
// production FrontendPathMedianLatency entry has expiresAfter: "2026-10-15"
// (Copilot review finding on PR #7124), so a wall-clock test would start
// failing on 2026-10-16 once the entry stops applying. The "before expiry"
// cases pin to a date clearly inside the window; a dedicated "after expiry"
// case pins past it and asserts the exception correctly stops suppressing.
func TestDefaultKnownIssuesClassification(t *testing.T) {
	t.Parallel()
	issues, err := parseKnownIssues(defaultKnownIssuesData)
	if err != nil {
		t.Fatalf("embedded knownIssues.yaml failed to parse: %v", err)
	}

	beforeExpiry := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	afterExpiry := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		at            time.Time
		alertName     string
		labels        map[string]string
		wantKnown     bool
		wantReasonSub string
	}{
		{
			name:          "median_latency_put_is_known",
			at:            beforeExpiry,
			alertName:     "FrontendPathMedianLatency",
			labels:        map[string]string{"method": "PUT", "route": "/clusters"},
			wantKnown:     true,
			wantReasonSub: "AROSLSRE-2232",
		},
		{
			name:          "median_latency_lowercase_put_is_known",
			at:            beforeExpiry,
			alertName:     "FrontendPathMedianLatency",
			labels:        map[string]string{"method": "put", "route": "/clusters"},
			wantKnown:     true,
			wantReasonSub: "AROSLSRE-2232",
		},
		{
			name:      "median_latency_get_is_not_known",
			at:        beforeExpiry,
			alertName: "FrontendPathMedianLatency",
			labels:    map[string]string{"method": "GET", "route": "/clusters"},
			wantKnown: false,
		},
		{
			name:      "median_latency_head_is_not_known",
			at:        beforeExpiry,
			alertName: "FrontendPathMedianLatency",
			labels:    map[string]string{"method": "HEAD", "route": "/clusters"},
			wantKnown: false,
		},
		{
			// After expiresAfter, the mutating-method skip must stop
			// applying so a still-firing alert fails CI again per the
			// documented behavior in docs/ci/operations.md.
			name:      "median_latency_put_after_expiry_is_not_known",
			at:        afterExpiry,
			alertName: "FrontendPathMedianLatency",
			labels:    map[string]string{"method": "PUT", "route": "/clusters"},
			wantKnown: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			classified := classifyAlertsAt([]alert{{Alert: alertData{Name: tt.alertName, Labels: tt.labels}}}, issues, tt.at)
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
