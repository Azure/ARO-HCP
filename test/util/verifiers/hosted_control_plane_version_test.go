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
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	configv1fake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
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
			expected: "4.21.13=Partial started=2026-05-06T01:24:16Z",
		},
		{
			// The history ARO-26775 had to be recovered from Kusto, because the test output
			// dropped exactly these stamps. Digests are the real ones from the ticket; the
			// registry host is a neutral placeholder because no rendering depends on it.
			name: "the ARO-26775 history, newest entry first",
			history: []configv1.UpdateHistory{
				{
					Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed,
					Image: "example.io/openshift-release-dev/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
				},
				{
					Version: "4.20.20", State: configv1.CompletedUpdate, StartedTime: started, CompletionTime: &completed,
					Image: "example.io/openshift-release-dev/ocp-release@sha256:1a59c7136e704fb5e0b4824b55757dac29b04c606ee2d05757a142cb90504b5d",
				},
			},
			expected: "4.21.13=Partial started=2026-05-06T01:24:16Z image=sha256:9d0436ae6f21; " +
				"4.20.20=Completed started=2026-05-06T00:36:22Z completed=2026-05-06T01:24:16Z image=sha256:1a59c7136e70",
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
		{
			// A time-only rendering of this history reads as completing 1h35m before it started.
			// status.history spans install through upgrade, so an overnight run hits it routinely.
			name: "an entry spanning UTC midnight still reads in order",
			history: []configv1.UpdateHistory{
				{
					Version:        "4.20.20",
					State:          configv1.CompletedUpdate,
					StartedTime:    mustTime(t, "2026-05-05T23:55:00Z"),
					CompletionTime: ptr.To(mustTime(t, "2026-05-06T00:20:00Z")),
				},
			},
			expected: "4.20.20=Completed started=2026-05-05T23:55:00Z completed=2026-05-06T00:20:00Z",
		},
		{
			// clusterversion stamps are UTC on the wire, but the renderer must not emit a local
			// offset if it is ever handed a non-UTC Time: RFC3339 renders one, and a mix of
			// offsets in one line cannot be compared by eye.
			name: "a non-UTC stamp is normalised to UTC",
			history: []configv1.UpdateHistory{
				{
					Version:     "4.20.20",
					State:       configv1.CompletedUpdate,
					StartedTime: metav1.NewTime(started.In(time.FixedZone("UTC+5", 5*60*60))),
				},
			},
			expected: "4.20.20=Completed started=2026-05-06T00:36:22Z",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := renderClusterVersionHistory(testCase.history); actual != testCase.expected {
				t.Errorf("renderClusterVersionHistory()\n  got:  %s\n  want: %s", actual, testCase.expected)
			}
		})
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
			image:    "example.io/openshift-release-dev/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
			expected: "sha256:9d0436ae6f21",
		},
		{
			name:     "digest shorter than 12 characters is not truncated",
			image:    "example.io/release@sha256:abc123",
			expected: "sha256:abc123",
		},
		{
			name:     "tag reference falls back to the last path segment",
			image:    "example.io/openshift-release-dev/ocp-release:4.21.13",
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

// TestUpgradeCheckErrorsCarryHistory pins the contract that every verdict an upgrade check reaches
// after reading clusterversion reports the state it judged.
//
// pollUntilReady surfaces the last check's error when it times out, so these strings are the whole
// of what a failing run leaves behind. The transient "the upgrade has not happened yet" verdicts
// matter most: they are what a 45 minute timeout reports, and without the stamps they cannot
// distinguish an upgrade that stalled midway from one that never started — the ARO-26775 failure
// mode. The terminal parse and downgrade verdicts are covered too, so the table fails if a branch
// is added that returns bare.
func TestUpgradeCheckErrorsCarryHistory(t *testing.T) {
	started := mustTime(t, "2026-05-06T00:36:22Z")
	completed := mustTime(t, "2026-05-06T01:24:16Z")
	installed := configv1.UpdateHistory{
		Version: "4.20.20", State: configv1.CompletedUpdate, StartedTime: started, CompletionTime: &completed,
	}
	upgrading := configv1.UpdateHistory{
		Version: "4.20.21", State: configv1.PartialUpdate, StartedTime: completed,
	}

	for _, testCase := range []struct {
		name            string
		history         []configv1.UpdateHistory
		conditions      []configv1.ClusterOperatorStatusCondition
		verifier        HostedClusterVerifier
		expectedVerdict string
	}{
		{
			name:            "z-stream, the upgrade has not happened yet",
			history:         []configv1.UpdateHistory{installed},
			verifier:        VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Minute),
			expectedVerdict: "no version in clusterversion/version status.history is greater than initial",
		},
		{
			name:            "z-stream, the install version is absent",
			history:         []configv1.UpdateHistory{upgrading},
			verifier:        VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Minute),
			expectedVerdict: "install version \"4.20.20\" not found",
		},
		{
			name:            "z-stream, an unparseable version",
			history:         []configv1.UpdateHistory{{Version: "not-a-version", State: configv1.PartialUpdate}},
			verifier:        VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Minute),
			expectedVerdict: "parse version \"not-a-version\" in history",
		},
		{
			name:            "z-stream, a y-stream jump",
			history:         []configv1.UpdateHistory{{Version: "4.21.0", State: configv1.PartialUpdate}, installed},
			verifier:        VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Minute),
			expectedVerdict: "different major.minor than initial",
		},
		{
			name:            "z-stream, a downgrade",
			history:         []configv1.UpdateHistory{{Version: "4.20.19", State: configv1.PartialUpdate}, installed},
			verifier:        VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Minute),
			expectedVerdict: "downgrade unexpected",
		},
		{
			name:            "y-stream, the target minor is absent",
			history:         []configv1.UpdateHistory{installed},
			verifier:        VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21", time.Minute),
			expectedVerdict: "no version in target minor \"4.21\"",
		},
		{
			name:            "y-stream, the previous minor is absent",
			history:         []configv1.UpdateHistory{{Version: "4.21.0", State: configv1.CompletedUpdate}},
			verifier:        VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21", time.Minute),
			expectedVerdict: "no version in previous minor \"4.20\"",
		},
		{
			name:            "y-stream, an unparseable version",
			history:         []configv1.UpdateHistory{{Version: "not-a-version", State: configv1.PartialUpdate}},
			verifier:        VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21", time.Minute),
			expectedVerdict: "parse clusterversion history version \"not-a-version\"",
		},
		{
			// The whole point of carrying Progressing: this verdict says the target minor is
			// missing, and the CVO says it is 150 of 900 through getting there. Reading one
			// without the other is what made ARO-26775 take four months.
			name:    "the Progressing message rides along with the verdict",
			history: []configv1.UpdateHistory{installed},
			conditions: []configv1.ClusterOperatorStatusCondition{{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Message: "Working towards 4.21.13: 150 of 900 done (16% complete)",
			}},
			verifier:        VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21", time.Minute),
			expectedVerdict: "150 of 900 done",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			status := configv1.ClusterVersionStatus{History: testCase.history, Conditions: testCase.conditions}
			configClient := configv1fake.NewClientset(&configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status:     status,
			}).ConfigV1()

			err := runCheckOnce(t, testCase.verifier, configClient)
			if err == nil {
				t.Fatalf("expected a verdict against history %v, got none", testCase.history)
			}
			if !strings.Contains(err.Error(), testCase.expectedVerdict) {
				t.Errorf("verdict\n  got:  %s\n  want it to contain: %s", err, testCase.expectedVerdict)
			}
			expectedSummary := clusterVersionSummary(status)
			if !strings.Contains(err.Error(), expectedSummary) {
				t.Errorf("verdict did not carry the state it judged\n  got:  %s\n  want it to contain: %s", err, expectedSummary)
			}
		})
	}
}

// runCheckOnce drives one poll iteration of an upgrade verifier's check against configClient,
// bypassing Verify so the test does not need a *rest.Config or the poll loop.
func runCheckOnce(t *testing.T, verifier HostedClusterVerifier, configClient configv1client.ConfigV1Interface) error {
	t.Helper()
	reporter := newUpgradePhaseReporter(verifier.Name())
	switch typedVerifier := verifier.(type) {
	case verifyHostedControlPlaneZStreamUpgradeOnly:
		initialSemver, err := semver.ParseTolerant(typedVerifier.initialVersion)
		if err != nil {
			t.Fatalf("parse initial version %q: %v", typedVerifier.initialVersion, err)
		}
		return typedVerifier.check(t.Context(), configClient, reporter, initialSemver)
	case verifyHostedControlPlaneYStreamUpgrade:
		previousMinor, err := semver.ParseTolerant(typedVerifier.previousMinor)
		if err != nil {
			t.Fatalf("parse previous minor %q: %v", typedVerifier.previousMinor, err)
		}
		targetMinor, err := semver.ParseTolerant(typedVerifier.targetMinor)
		if err != nil {
			t.Fatalf("parse target minor %q: %v", typedVerifier.targetMinor, err)
		}
		return typedVerifier.check(t.Context(), configClient, reporter, previousMinor, targetMinor)
	default:
		t.Fatalf("runCheckOnce does not know how to drive %T", verifier)
		return nil
	}
}

// TestUpgradePhaseReporterLogsOnlyOnChange pins the delta-only logging contract: repeated
// observations of an unchanged clusterversion must not re-log, or a 45 minute poll emits hundreds
// of identical lines and buries the transitions that matter.
//
// The assertion is on what the reporter actually emitted rather than on its recorded rendering,
// which stays put whether or not the guard is there. GinkgoLogr writes to GinkgoWriter too, so
// teeing the writer captures both outputs.
func TestUpgradePhaseReporterLogsOnlyOnChange(t *testing.T) {
	emitted := &bytes.Buffer{}
	ginkgo.GinkgoWriter.TeeTo(emitted)
	t.Cleanup(ginkgo.GinkgoWriter.ClearTeeWriters)

	reporter := newUpgradePhaseReporter("test-verifier")
	status := configv1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{{Version: "4.20.20", State: configv1.CompletedUpdate}},
	}

	reporter.observe(status)
	afterFirstObservation := emitted.String()
	if !strings.Contains(afterFirstObservation, "4.20.20=Completed") {
		t.Fatalf("expected the first observation to be logged, got: %q", afterFirstObservation)
	}

	reporter.observe(status)
	if repeated := emitted.String(); repeated != afterFirstObservation {
		t.Errorf("an unchanged clusterversion logged again:\n  first:  %q\n  second: %q", afterFirstObservation, repeated)
	}

	reporter.observe(configv1.ClusterVersionStatus{
		History: append([]configv1.UpdateHistory{
			{Version: "4.21.13", State: configv1.PartialUpdate},
		}, status.History...),
	})
	afterChange := strings.TrimPrefix(emitted.String(), afterFirstObservation)
	if !strings.Contains(afterChange, "4.21.13=Partial") {
		t.Errorf("expected a changed clusterversion to be logged, got: %q", afterChange)
	}
}
