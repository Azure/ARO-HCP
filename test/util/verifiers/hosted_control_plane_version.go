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

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// upgradePhaseReporter emits one line per observed change in clusterversion status.history,
// stamped with the elapsed time since the verifier started polling.
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

// observe logs the history only when its rendering differs from the previous observation, per
// the delta-only logging rule for polling verifiers in test/AGENTS.md.
func (r *upgradePhaseReporter) observe(history []configv1.UpdateHistory) {
	current := renderClusterVersionHistory(history)
	if current == r.previous {
		return
	}
	r.previous = current
	elapsed := time.Since(r.startTime).Round(time.Second)
	ginkgo.GinkgoLogr.Info("clusterversion history changed",
		"name", r.name, "elapsed", elapsed.String(), "history", current)
	ginkgo.GinkgoWriter.Printf("[%s] +%s %s\n", r.name, elapsed, current)
}

// renderClusterVersionHistory renders history as a single compact line, e.g.
// `4.21.13=Partial started=01:24:16; 4.20.20=Completed started=00:36:22 completed=01:24:16`.
func renderClusterVersionHistory(history []configv1.UpdateHistory) string {
	if len(history) == 0 {
		return "<empty>"
	}
	entries := make([]string, 0, len(history))
	for _, historyEntry := range history {
		entry := fmt.Sprintf("%s=%s", historyEntry.Version, historyEntry.State)
		if !historyEntry.StartedTime.IsZero() {
			entry += " started=" + historyEntry.StartedTime.UTC().Format(time.TimeOnly)
		}
		if historyEntry.CompletionTime != nil && !historyEntry.CompletionTime.IsZero() {
			entry += " completed=" + historyEntry.CompletionTime.UTC().Format(time.TimeOnly)
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, "; ")
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
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	reporter := newUpgradePhaseReporter(v.Name())
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, diagnoseClusterVersion,
		func(ctx context.Context) error {
			return v.check(ctx, configClient, reporter)
		})
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) check(ctx context.Context, configClient configv1client.ConfigV1Interface, reporter *upgradePhaseReporter) error {
	initialSemver, err := semver.ParseTolerant(v.initialVersion)
	if err != nil {
		return fmt.Errorf("parse initial version %q: %w", v.initialVersion, err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	reporter.observe(clusterVersion.Status.History)

	uniqueVersionSet := map[string]bool{}
	var uniqueVersions []string
	var sawInitial, sawUpgrade bool
	for _, history := range clusterVersion.Status.History {
		historyVersion, err := semver.ParseTolerant(history.Version)
		if err != nil {
			return fmt.Errorf("parse version %q in history: %w", history.Version, err)
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
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	reporter := newUpgradePhaseReporter(v.Name())
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, diagnoseClusterVersion,
		func(ctx context.Context) error {
			return v.check(ctx, configClient, reporter)
		})
}

func (v verifyHostedControlPlaneYStreamUpgrade) check(ctx context.Context, configClient configv1client.ConfigV1Interface, reporter *upgradePhaseReporter) error {
	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	reporter.observe(clusterVersion.Status.History)

	parsedPreviousMinor := metadataapi.Must(semver.ParseTolerant(v.previousMinor))
	parsedTargetMinor := metadataapi.Must(semver.ParseTolerant(v.targetMinor))

	var previousMinorFound, targetMinorFound bool
	for _, historyEntry := range clusterVersion.Status.History {
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
		return fmt.Errorf("clusterversion status.history has no version in previous minor %q; history is %s",
			v.previousMinor, renderClusterVersionHistory(clusterVersion.Status.History))
	}
	if !targetMinorFound {
		return fmt.Errorf("clusterversion status.history has no version in target minor %q; history is %s",
			v.targetMinor, renderClusterVersionHistory(clusterVersion.Status.History))
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
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig,
		DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			postUpgrade, err := clientset.Discovery().ServerVersion()
			if err != nil {
				return fmt.Errorf("get kube-apiserver ServerVersion: %w", err)
			}
			if reflect.DeepEqual(v.preUpgrade, postUpgrade) {
				return fmt.Errorf("kube-apiserver ServerVersion still %q, unchanged from pre-upgrade", postUpgrade.GitVersion)
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
