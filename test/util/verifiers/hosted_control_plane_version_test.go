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
			// dropped exactly these stamps. Images are the real digests from the ticket.
			name: "the ARO-26775 history, newest entry first",
			history: []configv1.UpdateHistory{
				{
					Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed,
					Image: "arohcpocpint.azurecr.io/openshift-release-dev/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
				},
				{
					Version: "4.20.20", State: configv1.CompletedUpdate, StartedTime: started, CompletionTime: &completed,
					Image: "arohcpocpint.azurecr.io/openshift-release-dev/ocp-release@sha256:1a59c7136e704fb5e0b4824b55757dac29b04c606ee2d05757a142cb90504b5d",
				},
			},
			expected: "4.21.13=Partial started=01:24:16 image=sha256:9d0436ae6f21; " +
				"4.20.20=Completed started=00:36:22 completed=01:24:16 image=sha256:1a59c7136e70",
		},
		{
			name: "entry with no timestamps or image renders version and state only",
			history: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			expected: "4.20.20=Completed",
		},
		{
			// Two entries can share a version and differ only by payload, which is why the
			// image is carried rather than dropped as the previous rendering did.
			name: "same version, different payloads, stays distinguishable",
			history: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, Image: "example.io/ocp-release@sha256:aaaaaaaaaaaabbbb"},
				{Version: "4.21.13", State: configv1.PartialUpdate, Image: "example.io/ocp-release@sha256:ccccccccccccdddd"},
			},
			expected: "4.21.13=Partial image=sha256:aaaaaaaaaaaa; 4.21.13=Partial image=sha256:cccccccccccc",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := renderClusterVersionHistory(testCase.history); actual != testCase.expected {
				t.Errorf("renderClusterVersionHistory()\n  got:  %s\n  want: %s", actual, testCase.expected)
			}
		})
	}
}

// TestPhaseLogObserveReportsOnlyChanges pins the delta-only logging rule in test/AGENTS.md:
// a verifier re-invoked by a polling loop must report transitions, not reprint an unchanged
// clusterversion. Over a 45 minute wait at a 2 minute interval the difference is ~22 identical
// lines versus one line per actual transition.
//
// The assertion is on observe's change signal rather than on captured output, so it needs no
// global writer state and fails if the comparison is removed.
func TestPhaseLogObserveReportsOnlyChanges(t *testing.T) {
	log := &phaseLog{}

	if !log.observe("m", "4.20.20=Completed") {
		t.Error("the first observation should be reported as a change")
	}
	if log.observe("m", "4.20.20=Completed") {
		t.Error("an unchanged observation was reported as a change, so it would be re-logged every poll")
	}
	if !log.observe("m", "4.21.13=Partial; 4.20.20=Completed") {
		t.Error("a changed observation should be reported")
	}
	if !log.observe("m", "4.20.20=Completed") {
		t.Error("returning to an earlier rendering is still a transition and should be reported")
	}
}

// TestPhaseLogObserveNilReceiver covers the degraded path: a verifier built without a phaseLog
// must still log rather than silently swallow every observation.
func TestPhaseLogObserveNilReceiver(t *testing.T) {
	var log *phaseLog
	if !log.observe("m", "4.20.20=Completed") {
		t.Error("a nil phaseLog should log unconditionally, not drop the observation")
	}
	if !log.observe("m", "4.20.20=Completed") {
		t.Error("a nil phaseLog has no memory, so a repeat should still log")
	}
}

// TestVerifierConstructorsAllocatePhaseLog guards the wiring: a constructor that forgets the
// phaseLog degrades silently to logging on every poll, which no other test would catch.
func TestVerifierConstructorsAllocatePhaseLog(t *testing.T) {
	yStream, ok := VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21").(verifyHostedControlPlaneYStreamUpgrade)
	if !ok {
		t.Fatal("VerifyHostedControlPlaneYStreamUpgrade returned an unexpected type")
	}
	if yStream.phaseLog == nil {
		t.Error("the y-stream verifier was built without a phaseLog, so it will re-log on every poll")
	}

	zStream, ok := VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20").(verifyHostedControlPlaneZStreamUpgradeOnly)
	if !ok {
		t.Fatal("VerifyHostedControlPlaneZStreamUpgradeOnly returned an unexpected type")
	}
	if zStream.phaseLog == nil {
		t.Error("the z-stream verifier was built without a phaseLog, so it will re-log on every poll")
	}
}

func TestShortImageDigest(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		image    string
		expected string
	}{
		{name: "empty", image: "", expected: ""},
		{
			name:     "digest is truncated to 12 characters",
			image:    "arohcpocpint.azurecr.io/openshift-release-dev/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
			expected: "sha256:9d0436ae6f21",
		},
		{
			name:     "digest shorter than 12 characters is not truncated",
			image:    "example.io/release@sha256:abc123",
			expected: "sha256:abc123",
		},
		{
			name:     "tag reference falls back to the last path segment",
			image:    "arohcpocpint.azurecr.io/openshift-release-dev/ocp-release:4.21.13",
			expected: "ocp-release:4.21.13",
		},
		{
			name:     "reference with no path separator is returned whole",
			image:    "ocp-release:4.21.13",
			expected: "ocp-release:4.21.13",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := shortImageDigest(testCase.image); actual != testCase.expected {
				t.Errorf("shortImageDigest(%q) = %q, want %q", testCase.image, actual, testCase.expected)
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
