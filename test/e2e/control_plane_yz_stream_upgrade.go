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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	It("should z-stream upgrade a cluster control plane that was previously y-stream upgraded",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "cp-yz-upgrade-hcp"
				channelGroup        = "candidate"
			)

			cpVersion, err := semver.ParseTolerant(framework.DefaultOpenshiftControlPlaneVersionId())
			if err != nil {
				Skip(fmt.Sprintf("failed to parse control plane version: %v", err))
			}
			if cpVersion.Minor == 0 {
				Skip("cannot compute previous minor from major.0; skipping y-stream upgrade test")
			}

			// cpMinor is the y-stream upgrade target (e.g. "4.22").
			cpMinor := fmt.Sprintf("%d.%d", cpVersion.Major, cpVersion.Minor)
			// installMinorLine is the minor one below cpMinor (e.g. "4.21").
			installMinorLine := fmt.Sprintf("%d.%d", cpVersion.Major, cpVersion.Minor-1)

			// Install at <minor>-1.latest (candidate channel, offset=0 is the latest z-stream).
			installVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, installMinorLine, 0)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve install version (%s.latest) for %s-%s: %v", installMinorLine, channelGroup, installMinorLine, err))
			}
			if installVersionId == "" {
				Skip(fmt.Sprintf("no install version (%s.latest) resolved for %s-%s", installMinorLine, channelGroup, installMinorLine))
			}

			// Y-stream upgrade target: <minor>.latest-1 (offset=1 is the second-latest z-stream on cpMinor).
			yStreamVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, cpMinor, 1)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve y-stream upgrade version (%s.latest-1) for %s-%s: %v", cpMinor, channelGroup, cpMinor, err))
			}
			if yStreamVersionId == "" {
				Skip(fmt.Sprintf("no y-stream upgrade version (%s.latest-1) resolved for %s-%s", cpMinor, channelGroup, cpMinor))
			}

			// Z-stream upgrade target: <minor>.latest (offset=0 is the latest z-stream on cpMinor).
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
			resourceGroup, err := tc.NewResourceGroup(ctx, "cp-yz-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group cp-yz-upgrade")

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
		})
})
