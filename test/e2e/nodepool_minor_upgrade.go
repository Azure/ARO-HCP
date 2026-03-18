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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade a nodepool from",
		func(ctx context.Context, nodePoolMinor string, nextMinor string) {
			const (
				customerClusterName  = "np-minor-upgrade-cluster-"
				customerNodePoolName = "npminorupgrade"
			)
			// TODO: decide if we want to use candidate channel group instead of stable for the nodepool upgrade
			channelGroup := framework.DefaultOCPChannelGroup

			nodePoolInitialVersion, hasUpgradePath, err := framework.GetLatestVersionInMinorWithUpgradePathTo(ctx, channelGroup, nodePoolMinor, nextMinor)
			Expect(err).NotTo(HaveOccurred())
			if !hasUpgradePath {
				Skip(fmt.Sprintf("no version in %s with upgrade path to %s", nodePoolMinor, nextMinor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			suffix := rand.String(6)
			clusterName := customerClusterName + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-minor-upgrade-"+suffix, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters at control plane version")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = nextMinor
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-minor-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-minor-" + suffix,
					"customerVnetName":       "customer-vnet-minor-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-minor-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the HCP cluster with version %s", nextMinor))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating nodepool with version %s (latest in minor with upgrade path to control plane)", nodePoolInitialVersion))
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = nodePoolInitialVersion
			nodePoolParams.ChannelGroup = channelGroup

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
			cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			var parseableVersions []string
			for _, h := range cv.Status.History {
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
				Skip(fmt.Sprintf("skipping: no Cincinnati upgrade path from nodepool version %s to any version <= %s (lowest control plane version); cannot exercise minor nodepool upgrade", nodePoolInitialVersion, parseableVersions[0]))
			}
			nodePoolDesiredVersion := candidates[len(candidates)-1].String()

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred())
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade to version %s", nodePoolDesiredVersion))
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolDesiredVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			updateResp, err := framework.UpdateNodePoolAndWait(ctx, nodePoolsClient, *resourceGroup.Name, clusterName, customerNodePoolName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodepool reports updated version")
			Expect(updateResp.Properties).NotTo(BeNil())
			Expect(updateResp.Properties.Version).NotTo(BeNil())
			Expect(updateResp.Properties.Version.ID).NotTo(BeNil())
			Expect(*updateResp.Properties.Version.ID).To(Equal(nodePoolDesiredVersion))

			By("verifying nodes are ready, updated to expected minor, and release images differ from pre-upgrade")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolDesiredVersion, customerNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, 45*time.Minute, 2*time.Minute).Should(Succeed())
		},
		Entry("4.20 to 4.21",
			labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible,
			"4.20", "4.21"),
	)
})
