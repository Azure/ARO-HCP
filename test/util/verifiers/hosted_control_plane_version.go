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
	"time"

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	v1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

func GetClusterVersion(ctx context.Context, adminRESTConfig *rest.Config) *v1.ClusterVersion {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		ginkgo.Fail("failed to create config client")
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		ginkgo.Fail("failed to get clusterversion from context")
	}

	return clusterVersion
}

func InitZStreamTest(ctx context.Context, adminRESTConfig *rest.Config, initialVersion string) ([]v1.UpdateHistory, semver.Version) {
	clusterVersion := GetClusterVersion(ctx, adminRESTConfig)

	ginkgo.GinkgoLogr.Info("Initial version state",
		"initialVersion", initialVersion,
		"history", framework.SummarizeClusterVersionHistory(clusterVersion.Status.History))

	initialSemver, err := semver.ParseTolerant(initialVersion)
	if err != nil {
		ginkgo.Fail("unable to parse initial version")
	}

	return clusterVersion.Status.History, initialSemver
}

func GetHistorySemver(version, initialVersion string, history []v1.UpdateHistory) semver.Version {
	if len(version) == 0 {
		ginkgo.Fail(fmt.Sprintf("UpdateHistory version found with length 0. initialVersion: %s, history: %v",
			initialVersion,
			framework.SummarizeClusterVersionHistory(history)))
	}

	historyVersion, err := semver.ParseTolerant(version)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "unable to parse version to semver",
			"version", version)
		ginkgo.Fail("unable to parse version to semver")
	}

	return historyVersion
}

type verifyHostedControlPlaneZStreamUpgradeTriggered struct {
	initialVersion string
}

func (v verifyHostedControlPlaneZStreamUpgradeTriggered) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneZStreamUpgradeTriggered(initial=%s)", v.initialVersion)
}

func (v verifyHostedControlPlaneZStreamUpgradeTriggered) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	ginkgo.GinkgoLogr.Info("Checking if z-stream upgrade has been triggered")

	history, initialSemver := InitZStreamTest(ctx, adminRESTConfig, v.initialVersion)

	if len(history) == 0 {
		ginkgo.Fail("Version history is empty. Cluster may not be properly initialized.")
	}
	if len(history) == 1 {
		return fmt.Errorf("z-stream upgrade has not yet been triggered. initial version: %q. History: %v",
			v.initialVersion,
			framework.SummarizeClusterVersionHistory(history))
	}

	// quickly validate the newest version, then return. The VerifyHostedControlPlaneZStreamUpgradeOnly test will more thoroughly vet each version history entry.
	latestHistoryEntrySemver := GetHistorySemver(history[0].Version, v.initialVersion, history)
	if !latestHistoryEntrySemver.GT(initialSemver) {
		ginkgo.Fail(fmt.Sprintf("Unexpected upgrade path occurred. initial version: %q. upgrade version: %q. History: %v",
			v.initialVersion,
			latestHistoryEntrySemver,
			framework.SummarizeClusterVersionHistory(history)))
	}

	return nil
}

// VerifyHostedControlPlaneZStreamUpgradeTriggered returns a verifier that checks if a z-stream
// upgrade has been triggered (but not necessarily completed).
func VerifyHostedControlPlaneZStreamUpgradeTriggered(initialVersion string) HostedClusterVerifier {
	return verifyHostedControlPlaneZStreamUpgradeTriggered{initialVersion: initialVersion}
}

type verifyHostedControlPlaneZStreamUpgradeOnly struct {
	initialVersion string
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Name() string {
	return fmt.Sprintf("VerifyHostedControlPlaneZStreamUpgradeOnly(initial=%s)", v.initialVersion)
}

func (v verifyHostedControlPlaneZStreamUpgradeOnly) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	ginkgo.GinkgoLogr.Info("Checking if z-stream upgrade has completed")

	history, initialSemver := InitZStreamTest(ctx, adminRESTConfig, v.initialVersion)

	if len(history) < 2 {
		ginkgo.Fail("z-stream upgrade not yet triggered. Cannot check if update has completed. " +
			"Ensure VerifyHostedControlPlaneZStreamUpgradeTriggered runs successfully before calling this test.")
	}

	initialSemverCount := 0
	for _, historyEntry := range history {
		historyEntrySemver := GetHistorySemver(historyEntry.Version, v.initialVersion, history)

		if historyEntrySemver.EQ(initialSemver) {
			initialSemverCount++
			if initialSemverCount > 1 {
				ginkgo.Fail(fmt.Sprintf("more than 1 of the initial version was found in the version history. initial version: %q. History: %v",
					v.initialVersion,
					framework.SummarizeClusterVersionHistory(history)))
			}

			continue
		}

		if historyEntrySemver.Major != initialSemver.Major ||
			historyEntrySemver.Minor != initialSemver.Minor {
			ginkgo.Fail(fmt.Sprintf("version %q in clusterversion history has different major.minor than initial %q (expected %d.%d.x)",
				historyEntrySemver.String(),
				v.initialVersion,
				initialSemver.Major,
				initialSemver.Minor))
		}

		if historyEntrySemver.LT(initialSemver) {
			ginkgo.Fail(fmt.Sprintf("downgrade unexpected: version %q is less than initial %q",
				historyEntrySemver.String(),
				initialSemver.String()))
		}

		if historyEntrySemver.GT(initialSemver) {
			if historyEntry.State == "Partial" {
				ginkgo.GinkgoLogr.Info("z-stream upgrade in progress",
					"initialVersion", initialSemver.String(),
					"upgradeVersion", historyEntrySemver.String(),
					"startedTime", historyEntry.StartedTime)
				return fmt.Errorf("z-stream upgrade in progress")
			}

			if historyEntry.State == "Completed" {
				var totalUpgradeTime time.Duration
				if historyEntry.CompletionTime != nil {
					totalUpgradeTime = historyEntry.CompletionTime.Sub(historyEntry.StartedTime.Time)
				}

				ginkgo.GinkgoLogr.Info("z-stream was upgraded successfully",
					"initialVersion", initialSemver.String(),
					"upgradeVersion", historyEntrySemver.String(),
					"startedTime", historyEntry.StartedTime,
					"completionTime", historyEntry.CompletionTime,
					"totalUpgradeTime", totalUpgradeTime)

				return nil
			}
		}
	}

	ginkgo.Fail("unexpected failure: no partial or completed z-stream upgrade found when validating version history array")
	return fmt.Errorf("unexpected failure: no partial or completed z-stream upgrade found when validating version history array") // make compiler happy
}

// VerifyHostedControlPlaneZStreamUpgradeOnly returns a verifier that checks if a z-stream
// upgrade has been completed, and that only the z-stream has been modified.
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
	clusterVersion := GetClusterVersion(ctx, adminRESTConfig)

	ginkgo.GinkgoLogr.Info("clusterversion status after y-stream upgrade",
		"history", framework.SummarizeClusterVersionHistory(clusterVersion.Status.History))

	parsedPreviousMinor := api.Must(semver.ParseTolerant(v.previousMinor))
	parsedTargetMinor := api.Must(semver.ParseTolerant(v.targetMinor))

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
