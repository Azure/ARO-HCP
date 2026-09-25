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

// TestUpgradePhaseReporterLogsOnlyOnChange pins the delta-only logging contract: repeated
// observations of an unchanged history must not re-log, or a 45 minute poll emits hundreds of
// identical lines and buries the transitions that matter.
func TestUpgradePhaseReporterLogsOnlyOnChange(t *testing.T) {
	reporter := newUpgradePhaseReporter("test-verifier")
	history := []configv1.UpdateHistory{
		{Version: "4.20.20", State: configv1.CompletedUpdate},
	}

	reporter.observe(history)
	firstRendering := reporter.previous
	if firstRendering == "" {
		t.Fatal("expected the first observation to be recorded")
	}

	reporter.observe(history)
	if reporter.previous != firstRendering {
		t.Errorf("an unchanged history altered the recorded rendering: %s", reporter.previous)
	}

	reporter.observe(append([]configv1.UpdateHistory{
		{Version: "4.21.13", State: configv1.PartialUpdate},
	}, history...))
	if reporter.previous == firstRendering {
		t.Error("expected a changed history to update the recorded rendering")
	}
}
