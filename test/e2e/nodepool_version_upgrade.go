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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade and update a nodepool",
		func(ctx context.Context, nodePoolMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			targetMinorVersion := api.Must(semver.ParseTolerant(targetMinor))
			nodePoolMinorVersion := api.Must(semver.ParseTolerant(nodePoolMinor))

			var (
				nodePoolInitialVersion string
				hasUpgradePath         bool
				err                    error
			)
			if nodePoolMinorVersion.EQ(targetMinorVersion) {
				// z-stream: same y.z line — older patch on node pool, cluster on latest patch.
				nodePoolInitialVersion, hasUpgradePath, err = framework.GetInstallVersionForZStreamUpgrade(ctx, channelGroup, targetMinor)
				if cincinnati.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for target minor %s on channel %s", targetMinor, channelGroup))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to determine z-stream install version for minor %s", targetMinor)
				if !hasUpgradePath {
					Skip(fmt.Sprintf("no z-stream upgrade path for minor %s on channel %s", targetMinor, channelGroup))
				}
			} else {
				// y-stream: node pool on an older minor than the cluster (target minor).
				Expect(nodePoolMinorVersion.LT(targetMinorVersion)).To(BeTrue(),
					"when nodePoolMinor and targetMinor differ, node pool minor must be less than target minor (y-stream)")
				nodePoolInitialVersion, hasUpgradePath, err = framework.GetLatestVersionInMinorWithUpgradePathTo(ctx, channelGroup, nodePoolMinor, targetMinor)
				if cincinnati.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for node pool minor %s on channel %s", nodePoolMinor, channelGroup))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to find version in minor %s with upgrade path to %s", nodePoolMinor, targetMinor)
				if !hasUpgradePath {
					Skip(fmt.Sprintf("no version in %s with upgrade path to target minor %s", nodePoolMinor, targetMinor))
				}
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
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, targetMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for target minor %s on channel %s", targetMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version in minor %s", targetMinor)
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-upgrade-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s with version %s", clusterName, clusterInstallVersion)

			By(fmt.Sprintf("creating nodepool with version %s (behind control plane; upgrade path validated via Cincinnati)", nodePoolInitialVersion))
			// Node pool name must be a DNS label (no '.'); encode minor as e.g. 4.20 -> npupgrade-4-20.
			customerNodePoolName := fmt.Sprintf("npupgrade-%s", strings.ReplaceAll(nodePoolMinor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInitialVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s with version %s", customerNodePoolName, nodePoolInitialVersion)

			By("getting admin credentials and lowest control plane version from OpenShift version history")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion resource")
			var parseableVersions []string
			for _, h := range clusterVersion.Status.History {
				if _, err := semver.ParseTolerant(h.Version); err != nil {
					continue
				}
				parseableVersions = append(parseableVersions, h.Version)
				if h.State == configv1.CompletedUpdate {
					break
				}
			}
			sort.Slice(parseableVersions, func(i, j int) bool {
				vi, _ := semver.ParseTolerant(parseableVersions[i])
				vj, _ := semver.ParseTolerant(parseableVersions[j])
				return vi.LT(vj)
			})
			Expect(parseableVersions).NotTo(BeEmpty(), "no clusterversion status.history entry with valid parseable version")

			By("computing nodepool desired version from Cincinnati (lowest <= lowest control plane version with upgrade path from nodepool)")
			candidates, err := framework.GetUpgradeCandidatesInMaxMinorFromCincinnati(ctx,
				channelGroup, parseableVersions[0], nodePoolInitialVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to get upgrade candidates from Cincinnati for node pool version %s", nodePoolInitialVersion)
			if len(candidates) == 0 {
				Skip(fmt.Sprintf("skipping: no Cincinnati upgrade path from nodepool version %s to any version <= %s (lowest control plane version in history); cannot exercise nodepool upgrade", nodePoolInitialVersion, parseableVersions[0]))
			}
			nodePoolDesiredVersion := candidates[len(candidates)-1].String()

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images before upgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade to version %s and update replicas to 3", nodePoolDesiredVersion))
			updateReplicas := 3
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: ptr.To(int32(updateReplicas)),
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
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
			npGetResponse, err := framework.GetNodePool20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
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

	// Nodepool upgrade without Cincinnati edge: proves the no-edge scenario works for forward
	// upgrades. HCP nodepools use Replace strategy — nodes are recreated, not upgraded in-place —
	// so Cincinnati upgrade edges are irrelevant. The backend only validates that the target
	// version exists in Cincinnati, not that an edge exists from the current version.
	DescribeTable("should upgrade a nodepool to a version without Cincinnati upgrade edge",
		func(ctx context.Context, minor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			fromVersion, toVersion, err := framework.GetVersionPairWithoutUpgradeEdge(ctx, channelGroup, minor)
			if errors.Is(err, framework.ErrNoEdgePairFound) {
				Skip(fmt.Sprintf("no version pair without upgrade edge in minor %s on channel %s", minor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get version pair without upgrade edge for minor %s", minor)

			clusterInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, minor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", minor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version in minor %s", minor)

			clusterVersion := api.Must(semver.ParseTolerant(clusterInstallVersion))
			Expect(toVersion.LTE(clusterVersion)).To(BeTrue(),
				"target version %s must not exceed control plane version %s", toVersion, clusterVersion)

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
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-np-ne-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", clusterName)

			By(fmt.Sprintf("creating nodepool at version %s (no Cincinnati edge to %s)", fromVersion, toVersion))
			customerNodePoolName := fmt.Sprintf("npnoedge-%s", strings.ReplaceAll(minor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = fromVersion.String()
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20240610(
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
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images for nodepool %s", customerNodePoolName)
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade from %s to %s (no Cincinnati edge)", fromVersion, toVersion))
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(toVersion.String()),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool %s from %s to %s", customerNodePoolName, fromVersion, toVersion)

			By("verifying nodes are recreated at the target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(toVersion.String(), customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET reflects the target version")
			npGetResponse, err := framework.GetNodePool20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(toVersion.String()), "nodepool %s version should be %s but got %s", customerNodePoolName, toVersion, *npGetResponse.Properties.Version.ID)
		},
		Entry("z-stream upgrade without Cincinnati edge in 4.20",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20"),
	)

	// Nodepool upgrade skipping one minor version (+2): the N-2 skew policy allows
	// kubelet to be 2 minor versions behind kube-apiserver, and HCP nodepools use Replace
	// strategy, so no step-through requirement exists.
	DescribeTable("should upgrade a nodepool skipping one minor version (+2)",
		func(ctx context.Context, nodePoolMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			// Workaround: skip on nightly until CS fixes the nightly→ACR image substitution
			// on the nodepool upgrade path (PatchNodePoolCR writes quay.io instead of ACR).
			// See ARO-27344.
			if channelGroup == "nightly" {
				Skip("nightly nodepool upgrade blocked by CS image substitution bug; see ARO-27344")
			}

			nodePoolInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, nodePoolMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", nodePoolMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version for nodepool minor %s", nodePoolMinor)

			clusterInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, targetMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", targetMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version for target minor %s", targetMinor)

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
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-sm-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s at version %s", clusterName, clusterInstallVersion)

			By(fmt.Sprintf("creating nodepool at version %s (2 minors behind CP %s)", nodePoolInstallVersion, clusterInstallVersion))
			customerNodePoolName := fmt.Sprintf("nps-%s-%s", strings.ReplaceAll(nodePoolMinor, ".", ""), strings.ReplaceAll(targetMinor, ".", ""))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s at version %s", customerNodePoolName, nodePoolInstallVersion)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images for nodepool %s", customerNodePoolName)
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool +2 minor upgrade from %s to %s", nodePoolInstallVersion, nodePoolDesiredVersion))
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool %s from %s to %s", customerNodePoolName, nodePoolInstallVersion, nodePoolDesiredVersion)

			By("verifying nodes are recreated at the target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET reflects the target version")
			npGetResponse, err := framework.GetNodePool20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
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
		func(ctx context.Context, minor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			versions, err := framework.GetAllVersionsInMinorStartingWith(ctx, channelGroup, minor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", minor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get versions for minor %s", minor)
			if len(versions) < 2 {
				Skip(fmt.Sprintf("fewer than 2 versions in minor %s on channel %s; cannot test downgrade", minor, channelGroup))
			}

			clusterInstallVersion := versions[0].String()
			nodePoolInstallVersion := versions[0].String()
			nodePoolDowngradeTarget := versions[1].String()

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
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-dg-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By(fmt.Sprintf("creating nodepool at latest version %s", nodePoolInstallVersion))
			customerNodePoolName := fmt.Sprintf("npdg-%s", strings.ReplaceAll(minor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s", customerNodePoolName)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("capturing node release images before downgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node release images before downgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before downgrade")

			By(fmt.Sprintf("triggering nodepool downgrade from %s to %s", nodePoolInstallVersion, nodePoolDowngradeTarget))
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDowngradeTarget),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to update nodepool %s to downgrade target %s", customerNodePoolName, nodePoolDowngradeTarget)

			By("verifying nodes are recreated at the downgrade target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDowngradeTarget, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed(), "node pool nodes were not recreated at downgrade target version %s", nodePoolDowngradeTarget)

			By("verifying node pool GET reflects the downgrade target version")
			npGetResponse, err := framework.GetNodePool20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
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
		func(ctx context.Context, cpMinor string, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			// Workaround: skip on nightly until CS fixes the nightly→ACR image substitution
			// on the nodepool upgrade path (PatchNodePoolCR writes quay.io instead of ACR).
			// See ARO-27344.
			if channelGroup == "nightly" {
				Skip("nightly nodepool downgrade blocked by CS image substitution bug; see ARO-27344")
			}

			clusterInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, cpMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", cpMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version for minor %s", cpMinor)

			nodePoolInstallVersion := clusterInstallVersion

			nodePoolDowngradeTarget, err := framework.GetLatestVersionInMinor(ctx, channelGroup, targetMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for minor %s on channel %s", targetMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get latest version for minor %s", targetMinor)

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
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-np-dgm-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By(fmt.Sprintf("creating nodepool at version %s (same as CP)", nodePoolInstallVersion))
			customerNodePoolName := fmt.Sprintf("npdg-%s-%s", strings.ReplaceAll(cpMinor, ".", ""), strings.ReplaceAll(targetMinor, ".", ""))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInstallVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s", customerNodePoolName)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("capturing node release images before downgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node release images before downgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before downgrade")

			By(fmt.Sprintf("triggering nodepool y-stream downgrade from %s to %s (-2 minors)", nodePoolInstallVersion, nodePoolDowngradeTarget))
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDowngradeTarget),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to update nodepool %s to downgrade target %s", customerNodePoolName, nodePoolDowngradeTarget)

			By("verifying nodes are recreated at the downgrade target version")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDowngradeTarget, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed(), "node pool nodes were not recreated at downgrade target version %s", nodePoolDowngradeTarget)

			By("verifying node pool GET reflects the downgrade target version")
			npGetResponse, err := framework.GetNodePool20240610(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s after downgrade", customerNodePoolName)
			Expect(npGetResponse.Properties).NotTo(BeNil(), "nodepool %s response Properties was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version).NotTo(BeNil(), "nodepool %s Properties.Version was nil", customerNodePoolName)
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil(), "nodepool %s Properties.Version.ID was nil", customerNodePoolName)
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDowngradeTarget), "nodepool %s version should be %s but got %s", customerNodePoolName, nodePoolDowngradeTarget, *npGetResponse.Properties.Version.ID)
		},
		Entry("from 4.21.zLatest to 4.19.zLatest",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.21", "4.19"),
	)
})
