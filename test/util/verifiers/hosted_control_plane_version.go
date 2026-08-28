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

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

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
		"history", framework.SummarizeClusterVersionHistory(clusterVersion.Status.History))

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
		"history", framework.SummarizeClusterVersionHistory(clusterVersion.Status.History))

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
		return fmt.Errorf("clusterversion status.history has no version in previous minor %q", v.previousMinor)
	}
	if !targetMinorFound {
		return fmt.Errorf("clusterversion status.history has no version in target minor %q", v.targetMinor)
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
		ginkgo.GinkgoLogr.Info("kube-apiserver ServerVersion unchanged from pre-upgrade",
			"preUpgrade", v.preUpgrade, "postUpgrade", postUpgrade)
		return fmt.Errorf("kube-apiserver ServerVersion not updated (unchanged from pre-upgrade)")
	}
	return nil
}

// VerifyKubeAPIServerServerVersionUpgraded fails if the kube-apiserver version is the same as before the upgrade.
// preUpgrade is the kubernetes discovery ServerVersion (/version) read from the cluster before upgrading.
func VerifyKubeAPIServerServerVersionUpgraded(preUpgrade *version.Info) HostedClusterVerifier {
	return verifyKubeAPIServerServerVersionUpgraded{preUpgrade: preUpgrade}
}
