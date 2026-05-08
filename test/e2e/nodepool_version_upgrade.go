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
				Expect(err).NotTo(HaveOccurred())
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
				Expect(err).NotTo(HaveOccurred())
				if !hasUpgradePath {
					Skip(fmt.Sprintf("no version in %s with upgrade path to target minor %s", nodePoolMinor, targetMinor))
				}
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			suffix := rand.String(6)
			clusterName := "np-version-upgrade-cluster-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-version-upgrade-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterInstallVersion, err := framework.GetLatestVersionInMinor(ctx, channelGroup, targetMinor)
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for target minor %s on channel %s", targetMinor, channelGroup))
			}
			Expect(err).NotTo(HaveOccurred())
			clusterParams.OpenshiftVersionId = clusterInstallVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-np-upgrade-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
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
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the HCP cluster with version %s", clusterInstallVersion))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating nodepool with version %s (behind control plane; upgrade path validated via Cincinnati)", nodePoolInitialVersion))
			// Node pool name must be a DNS label (no '.'); encode minor as e.g. 4.20 -> npupgrade-4-20.
			customerNodePoolName := fmt.Sprintf("npupgrade-%s", strings.ReplaceAll(nodePoolMinor, ".", "-"))
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInitialVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.NodeDrainTimeoutMinutes = to.Ptr(int32(10))
			err = tc.CreateNodePoolFromParam(
				ctx,
				*resourceGroup.Name,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials and lowest control plane version from OpenShift version history")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
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
			Expect(err).NotTo(HaveOccurred())
			if len(candidates) == 0 {
				Skip(fmt.Sprintf("skipping: no Cincinnati upgrade path from nodepool version %s to any version <= %s (lowest control plane version in history); cannot exercise nodepool upgrade", nodePoolInitialVersion, parseableVersions[0]))
			}
			nodePoolDesiredVersion := candidates[len(candidates)-1].String()

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred())
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
			_, err = framework.UpdateNodePoolAndWait(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes are ready, updated to expected version, and release images differ from pre-upgrade")
			// We have seen the backend take on the order of ~8 minutes to trigger the upgrade in CS; from there
			// the upgrade proceeds on its usual ~15–20 minute course. A 30 minute window left the test on the
			// edge of failing, so we use 45 minutes while investigating backend delay. Leads under discussion:
			// - Increase backend memory: https://github.com/Azure/ARO-HCP/pull/4581 , https://github.com/Azure/ARO-HCP/pull/4641
			// - Fire controllers sooner when Cosmos documents change: https://github.com/Azure/ARO-HCP/pull/4485 , https://github.com/Azure/ARO-HCP/pull/3913
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed())

			By("verifying node pool GET still reflects the new version")
			npGetResponse, err := framework.GetNodePool(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred())
			Expect(npGetResponse.Properties).NotTo(BeNil())
			Expect(npGetResponse.Properties.Version).NotTo(BeNil())
			Expect(npGetResponse.Properties.Version.ID).NotTo(BeNil())
			Expect(*npGetResponse.Properties.Version.ID).To(Equal(nodePoolDesiredVersion))

			By("verifying number of nodes ready and not draining meet the expected replicas")
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, updateReplicas).Verify(ctx, adminRESTConfig)).To(Succeed())

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
})
