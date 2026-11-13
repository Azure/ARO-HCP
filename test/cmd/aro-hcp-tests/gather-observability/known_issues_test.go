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
		name            string
		content         string
		wantErr         bool
		wantErrText     string
		wantLen         int
		wantMaxDuration time.Duration
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
			name:            "valid max duration",
			content:         "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  maxDuration: \"10m\"\n",
			wantLen:         1,
			wantMaxDuration: 10 * time.Minute,
		},
		{
			name:        "invalid max duration",
			content:     "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  maxDuration: \"ten minutes\"\n",
			wantErr:     true,
			wantErrText: `knownIssue 0 (SomeAlert): invalid maxDuration "ten minutes"`,
		},
		{
			name:        "negative max duration",
			content:     "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  maxDuration: \"-10m\"\n",
			wantErr:     true,
			wantErrText: `knownIssue 0 (SomeAlert): invalid maxDuration "-10m": must not be negative`,
		},
		{
			name:    "zero max duration",
			content: "knownIssues:\n- name: \"SomeAlert\"\n  reason: \"tracked\"\n  maxDuration: \"0s\"\n",
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
				if tt.wantErrText != "" && !strings.Contains(err.Error(), tt.wantErrText) {
					t.Errorf("error=%q, want it to contain %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != tt.wantLen {
				t.Errorf("got %d known issues, want %d", len(result), tt.wantLen)
			}
			if len(result) > 0 && result[0].maxDuration != tt.wantMaxDuration {
				t.Errorf("maxDuration=%v, want %v", result[0].maxDuration, tt.wantMaxDuration)
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

func TestClassifyAlertsMaxDuration(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	withinBound := started.Add(5 * time.Minute)
	atBound := started.Add(10 * time.Minute)
	overBound := atBound.Add(time.Nanosecond)
	beforeStart := started.Add(-time.Minute)
	now := started.Add(time.Hour)
	for _, tt := range []struct {
		name      string
		condition string
		startsAt  *time.Time
		endsAt    *time.Time
		unbounded bool
		known     bool
	}{
		{name: "resolved within bound", condition: "Resolved", startsAt: &started, endsAt: &withinBound, known: true},
		{name: "resolved at bound", condition: "Resolved", startsAt: &started, endsAt: &atBound, known: true},
		{name: "resolved over bound", condition: "Resolved", startsAt: &started, endsAt: &overBound},
		{name: "still firing", condition: "Fired", startsAt: &started},
		{name: "resolved timestamp without resolved condition", condition: "Fired", startsAt: &started, endsAt: &withinBound, known: true},
		{name: "resolved without end timestamp", condition: "Resolved", startsAt: &started},
		{name: "resolved without start timestamp", condition: "Resolved", endsAt: &withinBound},
		{name: "end before start", condition: "Resolved", startsAt: &started, endsAt: &beforeStart},
		{name: "no bound still firing", condition: "Fired", startsAt: &started, unbounded: true, known: true},
		{name: "no bound over ten minutes", condition: "Resolved", startsAt: &started, endsAt: &overBound, unbounded: true, known: true},
		{name: "no bound missing timestamps", condition: "Fired", unbounded: true, known: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := `knownIssues:
- name: "SomeAlert"
  reason: "transient provisioning"
`
			if !tt.unbounded {
				config += "  maxDuration: \"10m\"\n"
			}
			issues := mustParse(t, config)
			alerts := []alert{{Alert: alertData{
				Name:      "SomeAlert",
				Condition: tt.condition,
				StartsAt:  tt.startsAt,
				EndsAt:    tt.endsAt,
			}}}
			got := classifyAlertsAt(alerts, issues, now)
			if got[0].Metadata.KnownIssue != tt.known {
				t.Errorf("known=%v, want %v", got[0].Metadata.KnownIssue, tt.known)
			}
			wantReason := ""
			var wantFailed, wantSkipped uint
			if tt.known {
				wantReason = "transient provisioning"
				wantSkipped = 1
			} else {
				wantFailed = 1
			}
			if got[0].Metadata.KnownIssueReason != wantReason {
				t.Errorf("reason=%q, want %q", got[0].Metadata.KnownIssueReason, wantReason)
			}
			if alerts[0].Metadata.KnownIssue || alerts[0].Metadata.KnownIssueReason != "" {
				t.Error("classification mutated the input alert metadata")
			}
			workspace := &workspaceData{
				Type:        workspaceSvc,
				AlertRules:  []string{"SomeAlert"},
				FiredAlerts: got,
			}
			suite := workspaceDataToJUnit(logr.Discard(), workspace, timing.TimeWindow{})
			if suite.NumFailed != wantFailed || suite.NumSkipped != wantSkipped {
				t.Errorf("JUnit failures=%d skips=%d; want failures=%d skips=%d", suite.NumFailed, suite.NumSkipped, wantFailed, wantSkipped)
			}
		})
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
