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
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// upgradePhaseReporter emits one line per observed change in clusterversion state, stamped with
// the elapsed time since the verifier started polling.
//
// ARO-26775 took four months and six Kusto queries across three databases to attribute,
// because all a failing run left behind was "no version in target minor" after 2700s, with no
// indication of which phase consumed the budget. A run that logs its own phase transitions is
// diagnosable from the Prow log alone.
type upgradePhaseReporter struct {
	name      string
	startTime time.Time
	previous  string
}

func newUpgradePhaseReporter(name string) *upgradePhaseReporter {
	return &upgradePhaseReporter{name: name, startTime: time.Now()}
}

// observe logs the summary only when it differs from the previous observation, per the delta-only
// logging rule for polling verifiers in test/AGENTS.md. Each verifier owns its own reporter and
// drives it from a single poll loop, so no locking is needed.
func (r *upgradePhaseReporter) observe(status configv1.ClusterVersionStatus) {
	current := clusterVersionSummary(status)
	if current == r.previous {
		return
	}
	r.previous = current
	elapsed := time.Since(r.startTime).Round(time.Second)
	ginkgo.GinkgoLogr.Info("clusterversion changed",
		"name", r.name, "elapsed", elapsed.String(), "clusterversion", current)
	ginkgo.GinkgoWriter.Printf("[%s] +%s %s\n", r.name, elapsed, current)
}

// shortImageDigest reduces a release image reference to the leading 12 characters of its digest,
// which is enough to tell two payloads apart without putting a 100 character reference on every
// poll line. Falls back to the reference's last path segment when there is no digest.
func shortImageDigest(image string) string {
	if image == "" {
		return ""
	}
	if _, digest, found := strings.Cut(image, "@sha256:"); found {
		return "sha256:" + digest[:min(len(digest), 12)]
	}
	return image[strings.LastIndex(image, "/")+1:]
}

// renderClusterVersionHistory renders status.history as one compact line, e.g.
// `4.21.13=Partial started=2026-05-06T01:24:16Z image=sha256:9d0436ae6f21`.
//
// The stamps carry their date. status.history spans cluster install through upgrade completion, so
// a run that crosses UTC midnight would render a time-only `started=23:55:00 completed=00:20:00`
// that reads as finishing before it started — in the exact failure dump this rendering exists to
// make readable. RFC3339 is also the form the Kusto queries used to attribute ARO-26775 accept, so
// a stamp can be pasted from a Prow log straight into a query.
func renderClusterVersionHistory(history []configv1.UpdateHistory) string {
	if len(history) == 0 {
		return "<empty>"
	}
	entries := make([]string, 0, len(history))
	for _, historyEntry := range history {
		entry := fmt.Sprintf("%s=%s", historyEntry.Version, historyEntry.State)
		if !historyEntry.StartedTime.IsZero() {
			entry += " started=" + historyEntry.StartedTime.UTC().Format(time.RFC3339)
		}
		if historyEntry.CompletionTime != nil && !historyEntry.CompletionTime.IsZero() {
			entry += " completed=" + historyEntry.CompletionTime.UTC().Format(time.RFC3339)
		}
		if image := shortImageDigest(historyEntry.Image); image != "" {
			entry += " image=" + image
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, "; ")
}

// renderClusterVersionProgress returns the Progressing condition's message, which reports how far
// through the rollout the CVO is ("Working towards 4.21.13: 150 of 900 done").
func renderClusterVersionProgress(conditions []configv1.ClusterOperatorStatusCondition) string {
	for _, condition := range conditions {
		if condition.Type == configv1.OperatorProgressing {
			return fmt.Sprintf("Progressing=%s: %s", condition.Status, condition.Message)
		}
	}
	return ""
}

// clusterVersionSummary is the one-line state dump logged while waiting and appended to failures.
// History answers which phase the upgrade reached and when; Progressing answers how far through
// the current phase the CVO is, which is what separates a stalled rollout from a slow one.
func clusterVersionSummary(status configv1.ClusterVersionStatus) string {
	summary := "history is " + renderClusterVersionHistory(status.History)
	if progress := renderClusterVersionProgress(status.Conditions); progress != "" {
		summary += "; " + progress
	}
	return summary
}

// withClusterVersionSummary appends the state an upgrade verifier judged to the verdict it
// reached, and passes a passing verdict through untouched.
//
// pollUntilReady reports the last check's error when it times out, so whatever a check returns is
// what a failing run leaves in the Prow log. Wrapping once at the call site rather than at each
// branch means a branch cannot be added without its evidence: the transient
// "upgrade hasn't happened yet" verdicts are the ones a timeout actually surfaces, and those are
// exactly the ones that need the timestamps to tell a stalled upgrade from one that never started.
func withClusterVersionSummary(status configv1.ClusterVersionStatus, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w; %s", err, clusterVersionSummary(status))
}

// diagnoseClusterVersion dumps clusterversion status when an upgrade verifier times out, scoped
// to the resource being polled per the polling-diagnostics guidance in test/AGENTS.md.
func diagnoseClusterVersion(ctx context.Context, adminRESTConfig *rest.Config) string {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Sprintf("could not build a config client for diagnostics: %v", err)
	}
	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Sprintf("could not get clusterversion/version for diagnostics: %v", err)
	}
	details := []string{
		"clusterversion/version history: " + renderClusterVersionHistory(clusterVersion.Status.History),
		"clusterversion/version desired: " + clusterVersion.Status.Desired.Version,
	}
	for _, condition := range clusterVersion.Status.Conditions {
		details = append(details, fmt.Sprintf("condition %s=%s reason=%s: %s",
			condition.Type, condition.Status, condition.Reason, condition.Message))
	}
	return strings.Join(details, "\n")
}

type verifyHostedControlPlaneZStreamUpgradeOnly struct {
	initialVersion string
	timeout        time.Duration
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneZStreamUpgradeOnly(initial=%s)", v.initialVersion)
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	// Parse the caller's input before polling. It cannot become valid by waiting, so leaving it in
	// the poll callback would spend the whole timeout retrying a fixed error and then report it as
	// if the cluster had failed to upgrade.
	initialSemver, err := semver.ParseTolerant(v.initialVersion)
	if err != nil {
		return fmt.Errorf("parse initial version %q: %w", v.initialVersion, err)
	}
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	reporter := newUpgradePhaseReporter(v.Name())
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, diagnoseClusterVersion,
		func(ctx context.Context) error {
			return v.check(ctx, configClient, reporter, initialSemver)
		})
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) check(ctx context.Context, configClient configv1client.ConfigV1Interface, reporter *upgradePhaseReporter, initialSemver semver.Version) error {
	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	reporter.observe(clusterVersion.Status)

	return withClusterVersionSummary(clusterVersion.Status, v.evaluate(clusterVersion.Status.History, initialSemver))
}

// evaluate decides the z-stream verdict for one observation. It describes only what is wrong;
// check appends the state that was judged, so every branch reports the same evidence.
func (v verifyHostedControlPlaneZStreamUpgradeOnly) evaluate(history []configv1.UpdateHistory, initialSemver semver.Version) error {
	uniqueVersionSet := map[string]bool{}
	var uniqueVersions []string
	var sawInitial, sawUpgrade bool
	for _, historyEntry := range history {
		historyVersion, err := semver.ParseTolerant(historyEntry.Version)
		if err != nil {
			return fmt.Errorf("parse version %q in history: %w", historyEntry.Version, err)
		}
		if historyVersion.Major != initialSemver.Major || historyVersion.Minor != initialSemver.Minor {
			return fmt.Errorf("version %q in clusterversion history has different major.minor than initial %q (expected %d.%d.x)",
				historyVersion.String(), v.initialVersion, initialSemver.Major, initialSemver.Minor)
		}
		if historyVersion.LT(initialSemver) {
			return fmt.Errorf("downgrade unexpected: version %q is less than initial %q", historyVersion.String(), initialSemver.String())
		}
		if historyVersion.EQ(initialSemver) {
			sawInitial = true
		}
		if historyVersion.GT(initialSemver) {
			sawUpgrade = true
		}
		if key := historyVersion.String(); !uniqueVersionSet[key] {
			uniqueVersionSet[key] = true
			uniqueVersions = append(uniqueVersions, key)
		}
	}
	if !sawInitial {
		return fmt.Errorf("install version %q not found in clusterversion/version status.history; cannot confirm the z-stream upgrade started from it", v.initialVersion)
	}
	if !sawUpgrade {
		return fmt.Errorf("no version in clusterversion/version status.history is greater than initial %q", v.initialVersion)
	}
	// The automated z-stream upgrade must move the control plane off the pinned install version, so
	// the history must contain at least two unique versions: the install version and the latest
	// z-stream it was upgraded to.
	if len(uniqueVersions) < 2 {
		return fmt.Errorf("expected at least 2 unique versions in clusterversion/version status.history (install %q plus the auto-upgraded z-stream), got %d: %v",
			v.initialVersion, len(uniqueVersions), uniqueVersions)
	}
	return nil
}

// VerifyHostedControlPlaneZStreamUpgradeOnly returns a verifier that polls until the HCP control
// plane has performed only a z-stream upgrade from the initial version: every entry in
// ClusterVersion status.history has the same major.minor as initialVersion, the initial version
// itself appears in the history, at least one entry is greater than it, and the history holds at
// least two unique versions (the pinned install version plus the auto-upgraded latest z-stream).
// timeout must be > 0.
func VerifyHostedControlPlaneZStreamUpgradeOnly(initialVersion string, timeout time.Duration) HostedClusterVerifier {
	return verifyHostedControlPlaneZStreamUpgradeOnly{initialVersion: initialVersion, timeout: timeout}
}

type verifyHostedControlPlaneYStreamUpgrade struct {
	targetMinor   string
	previousMinor string
	timeout       time.Duration
}

func (v verifyHostedControlPlaneYStreamUpgrade) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneYStreamUpgrade(previousMinor=%s, targetMinor=%s)", v.previousMinor, v.targetMinor)
}

func (v verifyHostedControlPlaneYStreamUpgrade) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	// Parse the caller's input before polling, and as errors rather than panics. Neither value can
	// become valid by waiting, so parsing inside the poll callback would panic out of the loop on
	// the first check with a stack trace instead of naming the offending string.
	parsedPreviousMinor, err := semver.ParseTolerant(v.previousMinor)
	if err != nil {
		return fmt.Errorf("parse previous minor %q: %w", v.previousMinor, err)
	}
	parsedTargetMinor, err := semver.ParseTolerant(v.targetMinor)
	if err != nil {
		return fmt.Errorf("parse target minor %q: %w", v.targetMinor, err)
	}
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	reporter := newUpgradePhaseReporter(v.Name())
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, diagnoseClusterVersion,
		func(ctx context.Context) error {
			return v.check(ctx, configClient, reporter, parsedPreviousMinor, parsedTargetMinor)
		})
}

func (v verifyHostedControlPlaneYStreamUpgrade) check(ctx context.Context, configClient configv1client.ConfigV1Interface, reporter *upgradePhaseReporter, parsedPreviousMinor, parsedTargetMinor semver.Version) error {
	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	reporter.observe(clusterVersion.Status)

	return withClusterVersionSummary(clusterVersion.Status,
		v.evaluate(clusterVersion.Status.History, parsedPreviousMinor, parsedTargetMinor))
}

// evaluate decides the y-stream verdict for one observation. It describes only what is wrong;
// check appends the state that was judged, so every branch reports the same evidence.
func (v verifyHostedControlPlaneYStreamUpgrade) evaluate(history []configv1.UpdateHistory, parsedPreviousMinor, parsedTargetMinor semver.Version) error {
	var previousMinorFound, targetMinorFound bool
	for _, historyEntry := range history {
		if len(historyEntry.Version) == 0 {
			continue
		}
		version, err := semver.ParseTolerant(historyEntry.Version)
		if err != nil {
			return fmt.Errorf("parse clusterversion history version %q: %w", historyEntry.Version, err)
		}
		if version.Major == parsedPreviousMinor.Major && version.Minor == parsedPreviousMinor.Minor {
			previousMinorFound = true
		}
		if version.Major == parsedTargetMinor.Major && version.Minor == parsedTargetMinor.Minor {
			targetMinorFound = true
		}
	}
	if !previousMinorFound {
		return fmt.Errorf("clusterversion status.history has no version in previous minor %q", v.previousMinor)
	}
	if !targetMinorFound {
		return fmt.Errorf("clusterversion status.history has no version in target minor %q", v.targetMinor)
	}
	return nil
}

// VerifyHostedControlPlaneYStreamUpgrade returns a verifier that polls until clusterversion
// status.history contains at least one parseable version in previousMinor and at least one in
// targetMinor. timeout must be > 0.
func VerifyHostedControlPlaneYStreamUpgrade(previousMinor, targetMinor string, timeout time.Duration) HostedClusterVerifier {
	return verifyHostedControlPlaneYStreamUpgrade{previousMinor: previousMinor, targetMinor: targetMinor, timeout: timeout}
}

type verifyKubeAPIServerServerVersionUpgraded struct {
	preUpgrade *version.Info
	timeout    time.Duration
}

func (v verifyKubeAPIServerServerVersionUpgraded) Name() string {
	return "VerifyKubeAPIServerServerVersionUpgraded"
}

func (v verifyKubeAPIServerServerVersionUpgraded) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	clientset, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("create kubernetes clientset: %w", err)
	}
	// clusterversion is the state that explains why the kube-apiserver has not rolled yet, so it
	// is the targeted dump for this verifier's timeouts as well.
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, diagnoseClusterVersion,
		func(ctx context.Context) error {
			postUpgrade, err := framework.GetKubeAPIServerVersion(ctx, clientset.Discovery())
			if err != nil {
				return fmt.Errorf("get kube-apiserver ServerVersion: %w", err)
			}
			if reflect.DeepEqual(v.preUpgrade, postUpgrade) {
				// Naming the version is the point: the previous message said only "not updated",
				// which left a reader unable to tell a stalled rollout from a bad pre-upgrade
				// reading.
				return fmt.Errorf("expected the kube-apiserver ServerVersion to change from the pre-upgrade %q, still observing %q",
					v.preUpgrade.GitVersion, postUpgrade.GitVersion)
			}
			return nil
		})
}

// VerifyKubeAPIServerServerVersionUpgraded polls until the kube-apiserver version differs from
// the pre-upgrade value, and fails if it never does. preUpgrade is the kubernetes discovery
// ServerVersion (/version) read from the cluster before upgrading. timeout must be > 0.
func VerifyKubeAPIServerServerVersionUpgraded(preUpgrade *version.Info, timeout time.Duration) HostedClusterVerifier {
	return verifyKubeAPIServerServerVersionUpgraded{preUpgrade: preUpgrade, timeout: timeout}
}
