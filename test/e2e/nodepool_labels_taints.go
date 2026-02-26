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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	It("should be able to update node pool labels and taints",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "cluster-np-labels-taints"
				customerNodePoolName             = "np-1"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-np-labels-taints", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResource,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool with initial labels and taints")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName

			// using a smaller VM size for faster provisioning, experimental - needs more testing
			nodePoolParams.VMSize = "Standard_D4s_v3"

			nodePool := framework.BuildNodePoolFromParams(nodePoolParams, tc.Location())

			nodePool.Properties.Labels = []*hcpsdk20240610preview.Label{
				{
					Key:   to.Ptr("key1"),
					Value: to.Ptr("value1"),
				},
			}
			nodePool.Properties.Taints = []*hcpsdk20240610preview.Taint{
				{
					Key:    to.Ptr("key1"),
					Value:  to.Ptr("value1"),
					Effect: to.Ptr(hcpsdk20240610preview.EffectNoSchedule),
				},
			}

			_, err = framework.CreateNodePoolAndWait(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				nodePool,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for nodes to be ready")
			Eventually(func(ctx context.Context) error {
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10 * time.Minute).Should(Succeed())

			By("verifying initial labels are present on nodes")
			k8sClient, err := client.New(adminRESTConfig, client.Options{})
			Expect(err).NotTo(HaveOccurred())

			var nodeList corev1.NodeList
			err = k8sClient.List(ctx, &nodeList)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodeList.Items).NotTo(BeEmpty())

			Expect(framework.HasNodeLabel(nodeList.Items, "key1", "value1")).To(BeTrue(), "expected to find at least one node with label 'key1=value1'")

			By("verifying initial taints are present on nodes")
			Expect(framework.HasNodeTaint(nodeList.Items, "key1", "value1", corev1.TaintEffectNoSchedule)).To(BeTrue(), "expected to find at least one node with taint 'key1=value1:NoSchedule'")

			By("updating nodepool with a new label and scaling up")
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(3)),
					Labels: []*hcpsdk20240610preview.Label{
						{
							Key:   to.Ptr("key2"),
							Value: to.Ptr("value2"),
						},
					},
				},
			}

			_, err = framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying new label is present on newly created node")
			Eventually(func(ctx context.Context) bool {
				var nodeList corev1.NodeList
				err := k8sClient.List(ctx, &nodeList)
				if err != nil {
					return false
				}
				return framework.HasNodeLabel(nodeList.Items, "key2", "value2")
			}).WithContext(ctx).WithTimeout(15 * time.Minute).Should(BeTrue())

			By("updating nodepool with new taints and scaling up again")
			updateTaints := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(4)),
					Taints: []*hcpsdk20240610preview.Taint{
						{
							Key:    to.Ptr("key2"),
							Value:  to.Ptr("value2"),
							Effect: to.Ptr(hcpsdk20240610preview.EffectPreferNoSchedule),
						},
					},
				},
			}

			_, err = framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				updateTaints,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying new taint is present on newly created node")
			Eventually(func(ctx context.Context) bool {
				var nodeList corev1.NodeList
				err := k8sClient.List(ctx, &nodeList)
				if err != nil {
					return false
				}
				return framework.HasNodeTaint(nodeList.Items, "key2", "value2", corev1.TaintEffectPreferNoSchedule)
			}).WithContext(ctx).WithTimeout(15 * time.Minute).Should(BeTrue())

			By("verifying initial labels and taints are still present")
			var finalNodeList corev1.NodeList
			err = k8sClient.List(ctx, &finalNodeList)
			Expect(err).NotTo(HaveOccurred())

			Expect(framework.HasNodeLabel(finalNodeList.Items, "key1", "value1")).To(BeTrue(), "original label key1=value1 should still be present")
			Expect(framework.HasNodeTaint(finalNodeList.Items, "key1", "value1", corev1.TaintEffectNoSchedule)).To(BeTrue(), "original taint key1=value1:NoSchedule should still be present")

		})
})
