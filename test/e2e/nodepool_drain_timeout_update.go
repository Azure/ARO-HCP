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

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should update NodeDrainTimeoutMinutes and upgrade a nodepool",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			suffix := rand.String(6)

			clusterParams := framework.NewDefaultClusterParams()
			clusterName := "np-drain-timeout-and-upgrade-" + suffix
			clusterParams.ClusterName = clusterName

			channelGroup := clusterParams.ChannelGroup
			clusterVersion := api.Must(semver.ParseTolerant(clusterParams.OpenshiftVersionId))
			nodePoolMinorVersion := fmt.Sprintf("%d.%d", clusterVersion.Major, clusterVersion.Minor)

			By(fmt.Sprintf("determining node pool z-stream install version for %s on channel %s", nodePoolMinorVersion, channelGroup))
			nodePoolInitialVersion, hasUpgradePath, err := framework.GetInstallVersionForZStreamUpgrade(ctx, channelGroup, nodePoolMinorVersion)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for z-stream upgrade on minor %s channel %s", nodePoolMinorVersion, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to determine node pool z-stream install version for %s", nodePoolMinorVersion)

			if !hasUpgradePath {
				Skip(fmt.Sprintf("no z-stream upgrade path for minor %s on channel %s", nodePoolMinorVersion, channelGroup))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-drain-timeout-and-upgrade-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-np-drain-timeout-and-upgrade-" + suffix,
					"customerVnetName":       "customer-vnet-np-drain-timeout-and-upgrade-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-drain-timeout-and-upgrade-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterParams.OpenshiftVersionId))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with version %s", clusterParams.OpenshiftVersionId)

			By(fmt.Sprintf("creating nodepool with version %s and NodeDrainTimeoutMinutes=1", nodePoolInitialVersion))
			customerNodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInitialVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(1))
			err = tc.CreateNodePoolFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool with version %s and NodeDrainTimeoutMinutes=1", nodePoolInitialVersion)

			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("verifying NodeDrainTimeoutMinutes is 1 via GET")
			npResp, err := framework.GetNodePool(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %q", customerNodePoolName)
			Expect(npResp.Properties).NotTo(BeNil(), "node pool GET response Properties was nil")
			Expect(npResp.Properties.NodeDrainTimeoutMinutes).NotTo(BeNil(), "node pool GET response Properties.NodeDrainTimeoutMinutes was nil after creation")
			Expect(*npResp.Properties.NodeDrainTimeoutMinutes).To(Equal(int32(1)), "expected NodeDrainTimeoutMinutes to be 1 after creation")

			By("updating NodeDrainTimeoutMinutes from 1 to 0")
			drainUpdate := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					NodeDrainTimeoutMinutes: to.Ptr(int32(0)),
				},
			}
			_, err = framework.UpdateNodePoolAndWait(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, drainUpdate, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to update nodepool %q Properties.NodeDrainTimeoutMinutes to 0", customerNodePoolName)

			By("verifying NodeDrainTimeoutMinutes is 0 via GET")
			npResp, err = framework.GetNodePool(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %q", customerNodePoolName)
			Expect(npResp.Properties).NotTo(BeNil(), "node pool GET response Properties was nil")
			Expect(npResp.Properties.NodeDrainTimeoutMinutes).NotTo(BeNil(), "node pool GET response Properties.NodeDrainTimeoutMinutes was nil after update")
			Expect(*npResp.Properties.NodeDrainTimeoutMinutes).To(Equal(int32(0)), "expected NodeDrainTimeoutMinutes to be 0 after update")

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin credentials for cluster %s", clusterName)

			latestInNodePoolMinorVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, nodePoolMinorVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version in minor %s", nodePoolMinorVersion)

			By(fmt.Sprintf("computing nodepool z-stream upgrade target within minor %s (capped at %s)", nodePoolMinorVersion, latestInNodePoolMinorVersion))
			candidates, err := framework.GetUpgradeCandidatesInMaxMinorFromCincinnati(ctx,
				channelGroup, latestInNodePoolMinorVersion, nodePoolInitialVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to get upgrade candidates within %s from Cincinnati", nodePoolInitialVersion)
			if len(candidates) == 0 {
				Skip(fmt.Sprintf("no Cincinnati z-stream upgrade path from %s within minor %s", nodePoolInitialVersion, nodePoolMinorVersion))
			}
			nodePoolDesiredVersion := candidates[len(candidates)-1].String()

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images before upgrade for nodepool %s", customerNodePoolName)
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool version upgrade to %s", nodePoolDesiredVersion))
			versionUpdate := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, versionUpdate, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool %s to version %s", customerNodePoolName, nodePoolDesiredVersion)

			By("verifying nodes are ready, updated to expected version, and release images differ from pre-upgrade")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET still reflects the new version")
			npGetResponse, err := framework.GetNodePool(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %q", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "node pool GET response Properties was nil")
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "node pool GET response Properties.Version was nil")
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "node pool GET response Properties.Version.ID was nil")
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDesiredVersion), "expected node pool version to equal %s after upgrade", nodePoolDesiredVersion)

			By("verifying number of nodes ready and not draining meet the expected replicas")
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed(), "the number of nodes that are ready and not draining failed to meet the expected replicas of %d", nodePoolParams.Replicas)
		})
})
