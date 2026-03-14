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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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
			nodePoolParams.Replicas = int32(2)
			initialReplicas := nodePoolParams.Replicas

			// using a smaller VM size for faster provisioning
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
			k8sClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			nodeList := nodes.Items
			Expect(nodeList).NotTo(BeEmpty())
			Expect(len(nodeList)).To(Equal(int(initialReplicas)), "expected exactly %d initial nodes but found %d", int(initialReplicas), len(nodeList))

			Expect(framework.HasNodeLabel(nodeList, "key1", "value1", int(initialReplicas))).To(BeTrue(), "expected all nodes to have label 'key1=value1'")

			By("verifying initial taints are present on nodes")
			Expect(framework.HasNodeTaint(nodeList, "key1", "value1", corev1.TaintEffectNoSchedule, int(initialReplicas))).To(BeTrue(), "expected all nodes to have taint 'key1=value1:NoSchedule'")

			By("updating nodepool with new taints and scaling up")
			taintReplicas := int32(3)
			updateTaints := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(taintReplicas),
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
				20*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes are scaled to the expected count")
			Eventually(func(ctx context.Context) bool {
				nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					return false
				}
				return len(nodes.Items) == int(taintReplicas)
			}).WithContext(ctx).WithTimeout(15*time.Minute).Should(BeTrue(), "expected to have %d nodes", int(taintReplicas))

			By("verifying new taint is present on a node")
			Eventually(func(ctx context.Context) bool {
				nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					return false
				}
				return framework.HasNodeTaint(nodes.Items, "key2", "value2", corev1.TaintEffectPreferNoSchedule)
			}).WithContext(ctx).WithTimeout(15*time.Minute).Should(BeTrue(), "expected some node to have new taint key2=value2:PreferNoSchedule")

			By("updating nodepool with a new label and scaling up")
			finalReplicas := int32(4)
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(finalReplicas),
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

			By("verifying nodes are scaled to the expected count")
			Eventually(func(ctx context.Context) bool {
				nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					return false
				}
				return len(nodes.Items) == int(finalReplicas)
			}).WithContext(ctx).WithTimeout(15*time.Minute).Should(BeTrue(), "expected to have %d nodes", int(finalReplicas))

			By("verifying new label is present on newly created node")
			Eventually(func(ctx context.Context) bool {
				nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					return false
				}
				return framework.HasNodeLabel(nodes.Items, "key2", "value2")
			}).WithContext(ctx).WithTimeout(15 * time.Minute).Should(BeTrue())

			By("logging state of all nodes")
			finalNodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			finalNodeList := finalNodes.Items

			GinkgoLogr.Info("Final node state", "totalNodes", len(finalNodeList), "expectedNodes", int(finalReplicas))
			for _, node := range finalNodeList {
				GinkgoLogr.Info("Node", "name", node.Name, "labels", node.Labels, "taints", node.Spec.Taints)
			}

			By(fmt.Sprintf("verifying original labels persist on %d nodes", int(taintReplicas)))
			Expect(framework.HasNodeLabel(finalNodeList, "key1", "value1", int(taintReplicas))).To(BeTrue(), "expected %d nodes to have original label key1=value1", int(taintReplicas))

			By(fmt.Sprintf("verifying original taints persist on %d nodes", int(initialReplicas)))
			Expect(framework.HasNodeTaint(finalNodeList, "key1", "value1", corev1.TaintEffectNoSchedule, int(initialReplicas))).To(BeTrue(), "expected %d nodes to still have original taint key1=value1:NoSchedule", int(initialReplicas))
		})
})
