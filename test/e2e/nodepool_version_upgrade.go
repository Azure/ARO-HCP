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
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// resolveNodePoolTestVersion resolves an exact OpenShift version at the given z-stream offset from
// the tip of the channel for the minor, the same way the control plane upgrade tests select
// versions. Offset 0 is the channel tip, offset 1 the penultimate release, and so on. Nightly
// builds are not served by the update-service graph API, so only offset 0 (the latest accepted
// nightly) is available there; a non-zero nightly offset returns an empty string so the caller can
// Skip. An empty string with a nil error likewise means "no version resolved at this offset".
func resolveNodePoolTestVersion(ctx context.Context, channelGroup, minor string, offset uint) (string, error) {
	if channelGroup == "nightly" {
		if offset != 0 {
			return "", nil
		}
		return framework.GetLatestNightlyInstallVersion(ctx, channelGroup, minor)
	}
	release, err := controlplaneversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minor), offset)
	if err != nil {
		return "", err
	}
	if release == nil {
		return "", nil
	}
	return release.Version, nil
}

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade and update a nodepool",
		labels.MIContainers(1),
		func(ctx context.Context, nodePoolMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			targetMinorVersion := metadataapi.Must(semver.ParseTolerant(targetMinor))
			nodePoolMinorVersion := metadataapi.Must(semver.ParseTolerant(nodePoolMinor))
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Resolve versions the same way the control plane upgrade tests do. The control plane
			// installs at the channel tip for the target minor and the node pool upgrades to match it.
			clusterInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, targetMinor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve control plane version for %s-%s: %v", channelGroup, targetMinor, err))
			}
			if clusterInstallVersion == "" {
				Skip(fmt.Sprintf("no control plane version resolved for %s-%s", channelGroup, targetMinor))
			}
			nodePoolDesiredVersion := clusterInstallVersion

			// The node pool installs behind the control plane: one z-stream back for a z-stream
			// upgrade (same minor), or the tip of an older minor for a y-stream upgrade.
			var nodePoolInitialVersion string
			if nodePoolMinorVersion.EQ(targetMinorVersion) {
				nodePoolInitialVersion, err = resolveNodePoolTestVersion(ctx, channelGroup, targetMinor, normalOffset+1)
			} else {
				Expect(nodePoolMinorVersion.LT(targetMinorVersion)).To(BeTrue(),
					"when nodePoolMinor and targetMinor differ, node pool minor must be less than target minor (y-stream)")
				nodePoolInitialVersion, err = resolveNodePoolTestVersion(ctx, channelGroup, nodePoolMinor, normalOffset)
			}
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool install version on channel %s: %v", channelGroup, err))
			}
			if nodePoolInitialVersion == "" {
				Skip(fmt.Sprintf("no node pool install version resolved on channel %s for %s -> %s", channelGroup, nodePoolMinor, targetMinor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			suffix := rand.String(6)
			clusterName := "np-version-upgrade-cluster-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-version-upgrade-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for nodepool version upgrade")

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-upgrade-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-np-upgrade-" + suffix,
					"customerVnetName":       "customer-vnet-np-upgrade-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-upgrade-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s with version %s", clusterName, clusterInstallVersion)

			By(fmt.Sprintf("creating nodepool with version %s (behind control plane)", nodePoolInitialVersion))
			// Node pool name must be a DNS label (no '.'); encode minor as e.g. 4.20 -> npupgrade-4-20.
			customerNodePoolName := fmt.Sprintf("npupgrade-%s", strings.ReplaceAll(nodePoolMinor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInitialVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s with version %s", customerNodePoolName, nodePoolInitialVersion)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images before upgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade to version %s and update replicas to 3", nodePoolDesiredVersion))
			updateReplicas := 3
			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20260901preview.NodePoolUpdate{
				Properties: &hcpsdk20260901preview.NodePoolPropertiesUpdate{
					Replicas: ptr.To(int32(updateReplicas)),
					Version: &hcpsdk20260901preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade node pool %s to version %s", customerNodePoolName, nodePoolDesiredVersion)

			By("verifying nodes are ready, updated to expected version, and release images differ from pre-upgrade")
			// We have seen the backend take on the order of ~8 minutes to trigger the upgrade in CS; from there
			// the upgrade proceeds on its usual ~15–20 minute course. A 30 minute window left the test on the
			// edge of failing, so we use 45 minutes while investigating backend delay. Leads under discussion:
			// - Increase backend memory: https://github.com/Azure/ARO-HCP/pull/4581 , https://github.com/Azure/ARO-HCP/pull/4641
			// - Fire controllers sooner when Cosmos documents change: https://github.com/Azure/ARO-HCP/pull/4485 , https://github.com/Azure/ARO-HCP/pull/3913
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET still reflects the new version")
			npGetResponse, err := framework.GetNodePool20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET node pool %s after upgrade", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "node pool GET response Properties was nil")
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "node pool GET response Properties.Version was nil")
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "node pool GET response Properties.Version.ID was nil")
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDesiredVersion), "expected node pool version to equal %s after upgrade", nodePoolDesiredVersion)

			By("verifying number of nodes ready and not draining meet the expected replicas")
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, updateReplicas).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify %d ready and schedulable nodes for nodepool %s after upgrade", updateReplicas, customerNodePoolName)

		},
		Entry("from 4.20.z to 4.21.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20", "4.21"),
		Entry("from 4.21.z to 4.21.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.21", "4.21"),
		Entry("from 4.20.z to 4.20.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20", "4.20"),
	)

	// Nodepool z-stream upgrade: install the node pool one z-stream behind the channel tip and
	// upgrade it to the tip. HCP nodepools use Replace strategy — nodes are recreated, not upgraded
	// in-place — so a Cincinnati upgrade edge between the two versions is not required; the backend
	// only validates that the target version exists.
	DescribeTable("should upgrade a nodepool to a version without Cincinnati upgrade edge",
		labels.MIContainers(1),
		func(ctx context.Context, minor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Resolve versions the same way the control plane upgrade tests do: the control plane and
			// the upgrade target are the channel tip; the node pool installs one z-stream behind it.
			clusterInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, minor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve control plane version for %s-%s: %v", channelGroup, minor, err))
			}
			if clusterInstallVersion == "" {
				Skip(fmt.Sprintf("no control plane version resolved for %s-%s", channelGroup, minor))
			}
			toVersion := clusterInstallVersion
			fromVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, minor, normalOffset+1)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool install version for %s-%s: %v", channelGroup, minor, err))
			}
			if fromVersion == "" {
				Skip(fmt.Sprintf("no penultimate node pool version resolved for %s-%s", channelGroup, minor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			suffix := rand.String(6)
			clusterName := "np-noedge-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-noedge-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-np-ne-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-np-noedge-" + suffix,
					"customerVnetName":       "customer-vnet-np-noedge-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-noedge-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", clusterName)

			By(fmt.Sprintf("creating nodepool at version %s (one z-stream behind %s)", fromVersion, toVersion))
			customerNodePoolName := fmt.Sprintf("npnoedge-%s", strings.ReplaceAll(minor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = fromVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s at version %s", customerNodePoolName, fromVersion)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images for nodepool %s", customerNodePoolName)
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade from %s to %s", fromVersion, toVersion))
			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20260901preview.NodePoolUpdate{
				Properties: &hcpsdk20260901preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20260901preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(toVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool %s from %s to %s", customerNodePoolName, fromVersion, toVersion)

			By("verifying nodes are recreated at the target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(toVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET reflects the target version")
			npGetResponse, err := framework.GetNodePool20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(toVersion), "nodepool %s version should be %s but got %s", customerNodePoolName, toVersion, *npGetResponse.Properties.Version.ID)
		},
		Entry("z-stream upgrade without Cincinnati edge in 4.20",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20"),
	)

	// Nodepool upgrade skipping one minor version (+2): the N-2 skew policy allows
	// kubelet to be 2 minor versions behind kube-apiserver, and HCP nodepools use Replace
	// strategy, so no step-through requirement exists.
	DescribeTable("should upgrade a nodepool skipping one minor version (+2)",
		labels.MIContainers(1),
		func(ctx context.Context, nodePoolMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Resolve versions the same way the control plane upgrade tests do: the node pool and the
			// control plane each install at the tip of their minor. The node pool then upgrades +2
			// minors to match the control plane.
			nodePoolInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, nodePoolMinor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool version for %s-%s: %v", channelGroup, nodePoolMinor, err))
			}
			if nodePoolInstallVersion == "" {
				Skip(fmt.Sprintf("no node pool version resolved for %s-%s", channelGroup, nodePoolMinor))
			}

			clusterInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, targetMinor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve control plane version for %s-%s: %v", channelGroup, targetMinor, err))
			}
			if clusterInstallVersion == "" {
				Skip(fmt.Sprintf("no control plane version resolved for %s-%s", channelGroup, targetMinor))
			}

			nodePoolDesiredVersion := clusterInstallVersion

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			suffix := rand.String(6)
			clusterName := "np-skip-minor-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-skip-minor-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-sm-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        "customer-nsg-np-skip-" + suffix,
					"customerVnetName":       "customer-vnet-np-skip-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-skip-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s at version %s", clusterName, clusterInstallVersion)

			By(fmt.Sprintf("creating nodepool at version %s (2 minors behind CP %s)", nodePoolInstallVersion, clusterInstallVersion))
			customerNodePoolName := fmt.Sprintf("nps-%s-%s", strings.ReplaceAll(nodePoolMinor, ".", ""), strings.ReplaceAll(targetMinor, ".", ""))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s at version %s", customerNodePoolName, nodePoolInstallVersion)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images for nodepool %s", customerNodePoolName)
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool +2 minor upgrade from %s to %s", nodePoolInstallVersion, nodePoolDesiredVersion))
			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20260901preview.NodePoolUpdate{
				Properties: &hcpsdk20260901preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20260901preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool %s from %s to %s", customerNodePoolName, nodePoolInstallVersion, nodePoolDesiredVersion)

			By("verifying nodes are recreated at the target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET reflects the target version")
			npGetResponse, err := framework.GetNodePool20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDesiredVersion), "nodepool %s version should be %s but got %s", customerNodePoolName, nodePoolDesiredVersion, *npGetResponse.Properties.Version.ID)
		},
		Entry("from 4.20.z to 4.22.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20", "4.22"),
	)

	// Nodepool z-stream downgrade: proves the no-edge scenario works. Cincinnati has no
	// backward edges, so a downgrade exercises a version change without a Cincinnati upgrade
	// path. HCP nodepools use Replace strategy — nodes are recreated, not upgraded in-place.
	DescribeTable("should downgrade a nodepool version",
		labels.MIContainers(1),
		func(ctx context.Context, minor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Resolve versions the same way the control plane upgrade tests do: install the control
			// plane and node pool at the channel tip, then downgrade the node pool one z-stream back.
			// Cincinnati has no backward edges, so this exercises a version change without an upgrade
			// path; HCP node pools use Replace strategy so no edge is required.
			clusterInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, minor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve control plane version for %s-%s: %v", channelGroup, minor, err))
			}
			if clusterInstallVersion == "" {
				Skip(fmt.Sprintf("no control plane version resolved for %s-%s", channelGroup, minor))
			}
			nodePoolInstallVersion := clusterInstallVersion
			nodePoolDowngradeTarget, err := resolveNodePoolTestVersion(ctx, channelGroup, minor, normalOffset+1)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool downgrade target for %s-%s: %v", channelGroup, minor, err))
			}
			if nodePoolDowngradeTarget == "" {
				Skip(fmt.Sprintf("no penultimate version resolved for %s-%s; cannot test downgrade", channelGroup, minor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			suffix := rand.String(6)
			clusterName := "np-downgrade-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-downgrade-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-dg-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        "customer-nsg-np-dg-" + suffix,
					"customerVnetName":       "customer-vnet-np-dg-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-dg-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By(fmt.Sprintf("creating nodepool at latest version %s", nodePoolInstallVersion))
			customerNodePoolName := fmt.Sprintf("npdg-%s", strings.ReplaceAll(minor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s", customerNodePoolName)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("capturing node release images before downgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node release images before downgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before downgrade")

			By(fmt.Sprintf("triggering nodepool downgrade from %s to %s", nodePoolInstallVersion, nodePoolDowngradeTarget))
			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20260901preview.NodePoolUpdate{
				Properties: &hcpsdk20260901preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20260901preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(nodePoolDowngradeTarget),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to update nodepool %s to downgrade target %s", customerNodePoolName, nodePoolDowngradeTarget)

			By("verifying nodes are recreated at the downgrade target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDowngradeTarget, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(), "node pool nodes were not recreated at downgrade target version %s", nodePoolDowngradeTarget)

			By("verifying node pool GET reflects the downgrade target version")
			npGetResponse, err := framework.GetNodePool20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s after downgrade", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDowngradeTarget), "nodepool %s version should be %s but got %s", customerNodePoolName, nodePoolDowngradeTarget, *npGetResponse.Properties.Version.ID)
		},
		Entry("z-stream downgrade from 4.21.zLatest to 4.21.zPrevious",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.21"),
	)

	// Nodepool y-stream downgrade at the N-2 skew boundary: NP starts at the same minor
	// as CP, then downgrades 2 minors. The N-2 skew policy allows the node pool to be
	// 2 minor versions behind the control plane.
	DescribeTable("should downgrade a nodepool to a lower minor version",
		labels.MIContainers(1),
		func(ctx context.Context, cpMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Resolve versions the same way the control plane upgrade tests do: the control plane and
			// node pool install at the tip of cpMinor, then the node pool downgrades to the tip of the
			// lower targetMinor (-2 minors).
			clusterInstallVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, cpMinor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve control plane version for %s-%s: %v", channelGroup, cpMinor, err))
			}
			if clusterInstallVersion == "" {
				Skip(fmt.Sprintf("no control plane version resolved for %s-%s", channelGroup, cpMinor))
			}

			nodePoolInstallVersion := clusterInstallVersion

			nodePoolDowngradeTarget, err := resolveNodePoolTestVersion(ctx, channelGroup, targetMinor, normalOffset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool downgrade target for %s-%s: %v", channelGroup, targetMinor, err))
			}
			if nodePoolDowngradeTarget == "" {
				Skip(fmt.Sprintf("no node pool downgrade target resolved for %s-%s", channelGroup, targetMinor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			suffix := rand.String(6)
			clusterName := "np-dg-minor-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-dg-minor-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-dgm-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        "customer-nsg-np-dgm-" + suffix,
					"customerVnetName":       "customer-vnet-np-dgm-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-np-dgm-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By(fmt.Sprintf("creating nodepool at version %s (same as CP)", nodePoolInstallVersion))
			customerNodePoolName := fmt.Sprintf("npdg-%s-%s", strings.ReplaceAll(cpMinor, ".", ""), strings.ReplaceAll(targetMinor, ".", ""))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s", customerNodePoolName)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("capturing node release images before downgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node release images before downgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before downgrade")

			By(fmt.Sprintf("triggering nodepool y-stream downgrade from %s to %s (-2 minors)", nodePoolInstallVersion, nodePoolDowngradeTarget))
			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20260901preview.NodePoolUpdate{
				Properties: &hcpsdk20260901preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20260901preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(nodePoolDowngradeTarget),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to update nodepool %s to downgrade target %s", customerNodePoolName, nodePoolDowngradeTarget)

			By("verifying nodes are recreated at the downgrade target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDowngradeTarget, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(), "node pool nodes were not recreated at downgrade target version %s", nodePoolDowngradeTarget)

			By("verifying node pool GET reflects the downgrade target version")
			npGetResponse, err := framework.GetNodePool20260901(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s after downgrade", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDowngradeTarget), "nodepool %s version should be %s but got %s", customerNodePoolName, nodePoolDowngradeTarget, *npGetResponse.Properties.Version.ID)
		},
		Entry("from 4.22.zLatest to 4.20.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.22", "4.20"),
	)
})
