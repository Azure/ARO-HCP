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

package verifiers

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

func mustTime(t *testing.T, value string) metav1.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return metav1.NewTime(parsed)
}

func TestRenderClusterVersionHistory(t *testing.T) {
	started := mustTime(t, "2026-05-06T00:36:22Z")
	completed := mustTime(t, "2026-05-06T01:24:16Z")

	for _, testCase := range []struct {
		name     string
		history  []configv1.UpdateHistory
		expected string
	}{
		{
			name:     "empty history",
			history:  nil,
			expected: "<empty>",
		},
		{
			name: "in-progress entry has no completion stamp",
			history: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed},
			},
			expected: "4.21.13=Partial started=01:24:16",
		},
		{
			// The history ARO-26775 had to be recovered from Kusto, because the test output
			// dropped exactly these stamps.
			name: "the ARO-26775 history, newest entry first",
			history: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed},
				{Version: "4.20.20", State: configv1.CompletedUpdate, StartedTime: started, CompletionTime: &completed},
			},
			expected: "4.21.13=Partial started=01:24:16; 4.20.20=Completed started=00:36:22 completed=01:24:16",
		},
		{
			name: "entry with no timestamps renders version and state only",
			history: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			expected: "4.20.20=Completed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := renderClusterVersionHistory(testCase.history); actual != testCase.expected {
				t.Errorf("renderClusterVersionHistory()\n  got:  %s\n  want: %s", actual, testCase.expected)
			}
		})
	}
}

// TestClusterVersionSummaryIncludesProgress checks the Progressing message rides along with the
// history. It reports how far through the rollout the CVO is, which is the single line that says
// whether a stalled upgrade is stuck or merely slow.
func TestClusterVersionSummaryIncludesProgress(t *testing.T) {
	status := configv1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{
			{Version: "4.21.13", State: configv1.PartialUpdate},
		},
		Conditions: []configv1.ClusterOperatorStatusCondition{
			{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
			{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Message: "Working towards 4.21.13: 150 of 900 done (16% complete)",
			},
		},
	}

	summary := clusterVersionSummary(status)
	if !strings.Contains(summary, "4.21.13=Partial") {
		t.Errorf("expected the history in the summary, got: %s", summary)
	}
	if !strings.Contains(summary, "150 of 900 done") {
		t.Errorf("expected the Progressing message in the summary, got: %s", summary)
	}
}

// TestClusterVersionSummaryWithoutProgress guards the separator: with no Progressing condition the
// summary must not end in a dangling "; ".
func TestClusterVersionSummaryWithoutProgress(t *testing.T) {
	summary := clusterVersionSummary(configv1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{{Version: "4.20.20", State: configv1.CompletedUpdate}},
	})
	if strings.HasSuffix(summary, "; ") || strings.HasSuffix(summary, ";") {
		t.Errorf("summary ends in a dangling separator: %q", summary)
	}
	if summary != "history is 4.20.20=Completed" {
		t.Errorf("unexpected summary: %q", summary)
	}
}
