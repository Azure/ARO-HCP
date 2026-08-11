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
	"github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

// VerifyHostedControlPlaneZStreamUpgradeOnly returns a verifier that the HCP control plane has updated to the targetVersion,
// that only the z-stream portion of the version was modified, and that no downgrades occurred at any point in the upgrade history.
func VerifyHostedControlPlaneZStreamUpgradeOnly(initialVersion string, targetVersion string) HostedClusterVerifier {
	return verifyHostedControlPlaneZStreamUpgradeOnly{
		initialVersion: initialVersion,
		targetVersion:  targetVersion,
	}
}

type verifyHostedControlPlaneZStreamUpgradeOnly struct {
	initialVersion string
	targetVersion  string
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneZStreamUpgradeOnly(initialVersion=%s, targetVersion=%s)", v.initialVersion, v.targetVersion)
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	initialSemver, err := semver.ParseTolerant(v.initialVersion)
	if err != nil {
		return gomega.StopTrying(fmt.Sprintf("error parsing v.initialVersion: %q", v.initialVersion)).Wrap(err)
	}

	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return gomega.StopTrying("failed to create configClient").Wrap(err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterVersions from configClient: %w", err)
	}
	if len(clusterVersion.Status.History) < 1 {
		return fmt.Errorf("ClusterVersion.Status.History is empty; no upgrade history to verify")
	}

	ginkgo.GinkgoLogr.Info(
		"Retrieved openshift cluster version history",
		"history",
		framework.SummarizeClusterVersionHistory(clusterVersion.Status.History),
	)

	if clusterVersion.Status.History[len(clusterVersion.Status.History)-1].Version != v.initialVersion {
		return fmt.Errorf("cluster did not install to the correct install version. Expected: %s. Actual: %s",
			v.initialVersion,
			clusterVersion.Status.History[len(clusterVersion.Status.History)-1].Version,
		)
	}

	if clusterVersion.Status.History[0].Version != v.targetVersion {
		return fmt.Errorf("cluster did not upgrade to the correct target. Expected: %s. Actual: %s",
			v.targetVersion,
			clusterVersion.Status.History[0].Version,
		)
	}

	// note that clusterVersion.Status.History should have the latest version first and the install version last
	for i, history := range clusterVersion.Status.History {
		historyVersion, err := semver.ParseTolerant(history.Version)
		if err != nil {
			return gomega.StopTrying(fmt.Sprintf("error parsing history.Version: %q", history.Version)).Wrap(err)
		}

		if historyVersion.Major != initialSemver.Major || historyVersion.Minor != initialSemver.Minor {
			return gomega.StopTrying(
				fmt.Sprintf("clusterversion history contains at least one upgrade that altered the major or minor version. Expected: %d.%d.z. Actual: %s",
					initialSemver.Major,
					initialSemver.Minor,
					history.Version,
				),
			)
		}

		if i > 0 {
			// "prev" here means previously visited index
			prevHistoryVersion, err := semver.ParseTolerant(clusterVersion.Status.History[i-1].Version)
			if err != nil {
				return gomega.StopTrying(
					fmt.Sprintf("error parsing clusterVersion.Status.History[i-1].Version: %q",
						clusterVersion.Status.History[i-1].Version,
					),
				).Wrap(err)
			}

			// Given this test's setup, versions at earlier indices should be larger than or equal to versions at later indices. A rollback here is unexpected behavior.
			// Equal adjacent versions are allowed (e.g. a version re-applied after an aborted update) and are not treated as a downgrade.
			if prevHistoryVersion.LT(historyVersion) {
				return gomega.StopTrying(
					fmt.Sprintf(
						"downgrade unexpected: cluster went from version %q and unexpectedly downgraded to version %q. clusterVersion history: %v. Error when version at index %d downgraded to version at index %d",
						history.Version,
						prevHistoryVersion.String(),
						framework.SummarizeClusterVersionHistory(clusterVersion.Status.History),
						i,
						i-1,
					),
				)
			}
		}
	}

	return nil
}

// VerifyHostedControlPlaneYStreamUpgrade returns a verifier that clusterversion status.history
// contains at least one parseable version in previousMinor and at least one in targetMinor.
func VerifyHostedControlPlaneYStreamUpgrade(previousMinor, targetMinor string) HostedClusterVerifier {
	return verifyHostedControlPlaneYStreamUpgrade{previousMinor: previousMinor, targetMinor: targetMinor}
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

// VerifyKubeAPIServerServerVersionUpgraded fails if the kube-apiserver version is the same as before the upgrade.
// preUpgrade is the kubernetes discovery ServerVersion (/version) read from the cluster before upgrading.
func VerifyKubeAPIServerServerVersionUpgraded(preUpgrade *version.Info) HostedClusterVerifier {
	return verifyKubeAPIServerServerVersionUpgraded{preUpgrade: preUpgrade}
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
