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

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	// The matrix is a small, deliberate slice of the supported minors (4.20 through 5.0),
	// not the full product of every minor pair. Each entry installs at installMinorLine.latest,
	// y-stream upgrades to cpMinor.latest-1, then z-stream upgrades to cpMinor.latest.
	//
	// Entries whose minors are outside the current support window resolve no version and
	// self-skip via the guards below, so the table documents intended coverage (including the
	// cross-major 4.22 -> 5.0 hop) without failing on versions that are not yet published.
	DescribeTable("should z-stream upgrade a cluster control plane that was previously y-stream upgraded",
		labels.MIContainers(1),
		func(ctx context.Context, installMinorLine, cpMinor string) {
			const channelGroup = "candidate"

			versionLabel := strings.ReplaceAll(cpMinor, ".", "-")
			customerClusterName := "cp-yz-upgrade-" + versionLabel

			// Install at <installMinor>.latest (candidate channel, offset=0 is the latest z-stream).
			installVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, installMinorLine, 0)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve install version (%s.latest) for %s-%s: %v", installMinorLine, channelGroup, installMinorLine, err))
			}
			if installVersionId == "" {
				Skip(fmt.Sprintf("no install version (%s.latest) resolved for %s-%s", installMinorLine, channelGroup, installMinorLine))
			}

			// Y-stream upgrade target: <cpMinor>.latest-1 (offset=1 is the second-latest z-stream on cpMinor).
			yStreamVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, cpMinor, 1)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve y-stream upgrade version (%s.latest-1) for %s-%s: %v", cpMinor, channelGroup, cpMinor, err))
			}
			if yStreamVersionId == "" {
				Skip(fmt.Sprintf("no y-stream upgrade version (%s.latest-1) resolved for %s-%s", cpMinor, channelGroup, cpMinor))
			}

			// Z-stream upgrade target: <cpMinor>.latest (offset=0 is the latest z-stream on cpMinor).
			zStreamVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, cpMinor, 0)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve z-stream upgrade version (%s.latest) for %s-%s: %v", cpMinor, channelGroup, cpMinor, err))
			}
			if zStreamVersionId == "" {
				Skip(fmt.Sprintf("no z-stream upgrade version (%s.latest) resolved for %s-%s", cpMinor, channelGroup, cpMinor))
			}
			if yStreamVersionId == zStreamVersionId {
				Skip(fmt.Sprintf("y-stream and z-stream target versions are identical (%s); the %s channel has no distinct z-stream to upgrade to on minor %s", yStreamVersionId, channelGroup, cpMinor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cp-yz-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group cp-yz-upgrade-%s", versionLabel)

			By(fmt.Sprintf("creating cluster parameters at install version %s", installVersionId))
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster at version %s", installVersionId))
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s at version %s", customerClusterName, installVersionId)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By(fmt.Sprintf("y-stream upgrading the control plane from %s to %s", installVersionId, yStreamVersionId))
			// Encode the upgrade target through the control-plane pin tag. On nightly runs,
			// yStreamVersionId is a full build string, but Version.ID must be MAJOR.MINOR.
			// ControlPlaneExactVersionPatchTags returns the release line and clears any
			// existing exact-version pin so the old install version does not block the upgrade.
			yVersionLine, yUpgradeTags := framework.ControlPlaneExactVersionPatchTags(yStreamVersionId)
			yStreamUpdate := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
				Tags: framework.TagsForPatch(clusterParams.Tags, yUpgradeTags),
				Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20260901preview.VersionProfileUpdate{
						ID:           to.Ptr(yVersionLine),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20260901(ctx, hcpClient, *resourceGroup.Name, customerClusterName, yStreamUpdate, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "y-stream upgrade of cluster %s to %s did not complete (ARM LRO failed or hung)", customerClusterName, yStreamVersionId)

			By(fmt.Sprintf("verifying the control plane advanced to the y-stream version %s", yStreamVersionId))
			Eventually(func(ctx context.Context) error {
				return verifiers.VerifyHostedControlPlaneYStreamUpgrade(installMinorLine, cpMinor).Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).
				Should(Succeed(), "control plane did not y-stream upgrade from minor %s to %s on cluster %s", installMinorLine, cpMinor, customerClusterName)

			By(fmt.Sprintf("z-stream upgrading the control plane from %s to %s", yStreamVersionId, zStreamVersionId))
			// Clear the y-stream exact-version pin and encode the z-stream target.
			zVersionLine, zUpgradeTags := framework.ControlPlaneExactVersionPatchTags(zStreamVersionId)
			zStreamUpdate := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
				Tags: framework.TagsForPatch(clusterParams.Tags, zUpgradeTags),
				Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20260901preview.VersionProfileUpdate{
						ID:           to.Ptr(zVersionLine),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20260901(ctx, hcpClient, *resourceGroup.Name, customerClusterName, zStreamUpdate, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "z-stream upgrade of cluster %s to %s did not complete (ARM LRO failed or hung)", customerClusterName, zStreamVersionId)

			By(fmt.Sprintf("verifying the control plane advanced to the z-stream version %s", zStreamVersionId))
			Eventually(func(ctx context.Context) error {
				return verifiers.VerifyHostedControlPlaneReachedVersion(zStreamVersionId).Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).
				Should(Succeed(), "control plane did not reach z-stream version %s on cluster %s", zStreamVersionId, customerClusterName)
		},
		// Covered today (4.20 through 5.0 are in the support window):
		Entry("from 4.20 minor to 4.21 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.20", "4.21"),
		Entry("from 4.21 minor to 4.22 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.21", "4.22"),
		Entry("from 4.22 minor to 5.0 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22", "5.0"),
		// Documented but currently self-skipping (target minor not yet published):
		Entry("from 4.22 minor to 4.23 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22", "4.23"),
		Entry("from 4.23 minor to 5.1 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.23", "5.1"),
		Entry("from 5.0 minor to 5.1 minor", labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.0", "5.1"),
	)
})
