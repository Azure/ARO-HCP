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

// renderClusterVersionHistory renders status.history as one compact line, e.g.
// `4.21.13=Partial started=01:24:16; 4.20.20=Completed started=00:36:22 completed=01:24:16`.
//
// The started/completed stamps are what ARO-26775 needed to attribute a 45 minute timeout to a
// phase, and are exactly what the previous rendering dropped.
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

// clusterVersionSummary is the one-line state dump logged while waiting and appended to failures,
// so a Prow log alone shows which phase consumed the budget.
func clusterVersionSummary(status configv1.ClusterVersionStatus) string {
	summary := "history is " + renderClusterVersionHistory(status.History)
	if progress := renderClusterVersionProgress(status.Conditions); progress != "" {
		summary += "; " + progress
	}
	return summary
}

type verifyHostedControlPlaneZStreamUpgradeOnly struct {
	initialVersion string
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneZStreamUpgradeOnly(initial=%s)", v.initialVersion)
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	initialSemver, err := semver.ParseTolerant(v.initialVersion)
	if err != nil {
		return fmt.Errorf("parse initial version %q: %w", v.initialVersion, err)
	}

	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	ginkgo.GinkgoLogr.Info("Retrieved openshift cluster version history",
		"clusterversion", clusterVersionSummary(clusterVersion.Status))

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
		return fmt.Errorf("install version %q not found in clusterversion/version status.history; cannot confirm the z-stream upgrade started from it; %s",
			v.initialVersion, clusterVersionSummary(clusterVersion.Status))
	}
	if !sawUpgrade {
		return fmt.Errorf("no version in clusterversion/version status.history is greater than initial %q; %s",
			v.initialVersion, clusterVersionSummary(clusterVersion.Status))
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

// VerifyHostedControlPlaneZStreamUpgradeOnly returns a verifier that the HCP control plane has
// performed only a z-stream upgrade from the initial version: every entry in ClusterVersion
// status.history has the same major.minor as initialVersion, the initial version itself appears in
// the history, at least one entry is greater than it, and the history holds at least two unique
// versions (the pinned install version plus the auto-upgraded latest z-stream).
func VerifyHostedControlPlaneZStreamUpgradeOnly(initialVersion string) HostedClusterVerifier {
	return verifyHostedControlPlaneZStreamUpgradeOnly{initialVersion: initialVersion}
}

type verifyHostedControlPlaneYStreamUpgrade struct {
	targetMinor   string
	previousMinor string
}

func (v verifyHostedControlPlaneYStreamUpgrade) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneYStreamUpgrade(previousMinor=%s, targetMinor=%s)", v.previousMinor, v.targetMinor)
}

func (v verifyHostedControlPlaneYStreamUpgrade) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
	}

	ginkgo.GinkgoLogr.Info("clusterversion status after y-stream upgrade",
		"clusterversion", clusterVersionSummary(clusterVersion.Status))

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
		return fmt.Errorf("clusterversion status.history has no version in previous minor %q; %s",
			v.previousMinor, clusterVersionSummary(clusterVersion.Status))
	}
	if !targetMinorFound {
		return fmt.Errorf("clusterversion status.history has no version in target minor %q; %s",
			v.targetMinor, clusterVersionSummary(clusterVersion.Status))
	}
	return nil
}

// VerifyHostedControlPlaneYStreamUpgrade returns a verifier that clusterversion status.history
// contains at least one parseable version in previousMinor and at least one in targetMinor.
func VerifyHostedControlPlaneYStreamUpgrade(previousMinor, targetMinor string) HostedClusterVerifier {
	return verifyHostedControlPlaneYStreamUpgrade{previousMinor: previousMinor, targetMinor: targetMinor}
}

type verifyKubeAPIServerServerVersionUpgraded struct {
	preUpgrade *version.Info
}

func (v verifyKubeAPIServerServerVersionUpgraded) Name() string {
	return "VerifyKubeAPIServerServerVersionUpgraded"
}

func (v verifyKubeAPIServerServerVersionUpgraded) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	clientset, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("create kubernetes clientset: %w", err)
	}
	postUpgrade, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("get kube-apiserver ServerVersion: %w", err)
	}
	if reflect.DeepEqual(v.preUpgrade, postUpgrade) {
		// Naming the version is the point: the previous message said only "not updated", which
		// left a reader unable to tell a stalled rollout from a bad pre-upgrade reading.
		return fmt.Errorf("expected the kube-apiserver ServerVersion to change from the pre-upgrade %q, still observing %q",
			v.preUpgrade.GitVersion, postUpgrade.GitVersion)
	}
	return nil
}

// VerifyKubeAPIServerServerVersionUpgraded fails if the kube-apiserver version is the same as before the upgrade.
// preUpgrade is the kubernetes discovery ServerVersion (/version) read from the cluster before upgrading.
func VerifyKubeAPIServerServerVersionUpgraded(preUpgrade *version.Info) HostedClusterVerifier {
	return verifyKubeAPIServerServerVersionUpgraded{preUpgrade: preUpgrade}
}
