// Copyright 2025 Microsoft Corporation
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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should be able to list nodepools by cluster, resource group and subscription",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		func(ctx context.Context) {
			tc := framework.NewTestContext()
			clusterName := "list-test-nodepool-" + rand.String(6)
			nodePoolName1 := "np-1"
			nodePoolName2 := "np-2"

			// ========================================
			// PHASE 0: Create a resource group and cluster
			// ========================================

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepools-listing", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("deploying cluster-only template (infra + identities + cluster, no nodepools)")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"cluster-only",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json")),
				map[string]interface{}{
					"clusterName":     clusterName,
					"persistTagValue": false,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			npClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			resourcesClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewClient()

			// ========================================
			// PHASE 1: Verify empty nodepool listings
			// ========================================

			By("listing nodepools by cluster - should be empty")
			clusterPager := npClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			nodePoolCount := 0
			for clusterPager.More() {
				nodePoolList, err := clusterPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				nodePoolCount += len(nodePoolList.Value)
			}
			Expect(nodePoolCount).To(Equal(0), "Expected no nodepools in cluster without nodepools")

			By("listing nodepools by resource group - should be empty")
			resourcesGroupPager := resourcesClient.NewListByResourceGroupPager(*resourceGroup.Name, &armresources.ClientListByResourceGroupOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			for resourcesGroupPager.More() {
				nodePoolList, err := resourcesGroupPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				nodePoolCount += len(nodePoolList.Value)
			}
			Expect(nodePoolCount).To(Equal(0), "Expected no nodepools in RG")

			By("listing nodepools by subscription - should find none from created cluster")
			subscriptionPager := resourcesClient.NewListPager(&armresources.ClientListOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			foundOurClusterNodePool := false
			for subscriptionPager.More() {
				nodePoolList, err := subscriptionPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil && strings.HasPrefix(*np.Name, clusterName+"/") {
						foundOurClusterNodePool = true
						break
					}
				}
				if foundOurClusterNodePool {
					break
				}
			}
			Expect(foundOurClusterNodePool).To(BeFalse(), "Expected no nodepools from created cluster in subscription listing")

			// ========================================
			// PHASE 2: Create a nodepool
			// ========================================

			By("creating nodepool with 2 replicas")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"nodepool-deployment",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]interface{}{
					"clusterName":  clusterName,
					"nodePoolName": nodePoolName1,
					"replicas":     2,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying first nodepool is created")
			_, err = framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName1,
			)
			Expect(err).NotTo(HaveOccurred())

			// ========================================
			// PHASE 3: Verify created nodepool appears in all listings
			// ========================================

			By("listing nodepools by cluster - should find 1 created nodepool")
			clusterPager = npClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			nodePoolCount = 0
			foundCreatedNodePool := false
			for clusterPager.More() {
				nodePoolList, err := clusterPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				nodePoolCount += len(nodePoolList.Value)
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil && *np.Name == nodePoolName1 {
						foundCreatedNodePool = true
					}
				}
			}
			Expect(nodePoolCount).To(Equal(1), "Expected one listed nodepool")
			Expect(foundCreatedNodePool).To(BeTrue(), "Expected to find created nodepool %s in the list", nodePoolName1)

			By("listing nodepools by resource group - should find 1 created nodepool")
			resourcesGroupPager = resourcesClient.NewListByResourceGroupPager(*resourceGroup.Name, &armresources.ClientListByResourceGroupOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			nodePoolCount = 0
			foundCreatedNodePool = false
			for resourcesGroupPager.More() {
				nodePoolList, err := resourcesGroupPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				nodePoolCount += len(nodePoolList.Value)
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					// Nodepool name is in the format: "clustername/nodepoolname"
					if np.Name != nil && *np.Name == clusterName+"/"+nodePoolName1 {
						foundCreatedNodePool = true
					}
				}
			}
			Expect(nodePoolCount).To(Equal(1), "Expected one listed nodepool")
			Expect(foundCreatedNodePool).To(BeTrue(), "Expected to find created nodepool %s in the list", nodePoolName1)

			By("listing nodepools by subscription - should find first created nodepool")
			subscriptionPager = resourcesClient.NewListPager(&armresources.ClientListOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			foundCreatedNodePool = false
			for subscriptionPager.More() {
				nodePoolList, err := subscriptionPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					// Nodepool name is in the format: "clustername/nodepoolname"
					if np.Name != nil && *np.Name == clusterName+"/"+nodePoolName1 {
						foundCreatedNodePool = true
						break
					}
				}
				if foundCreatedNodePool {
					break
				}
			}
			Expect(foundCreatedNodePool).To(BeTrue(), "Expected to find nodepool %s from cluster %s in subscription listing", nodePoolName1, clusterName)

			// ========================================
			// PHASE 4: Create second nodepool
			// ========================================

			By("creating second nodepool with 1 replica")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"nodepool-deployment",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]interface{}{
					"clusterName":  clusterName,
					"nodePoolName": nodePoolName2,
					"replicas":     1,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying second nodepool is created")
			_, err = framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName2,
			)
			Expect(err).NotTo(HaveOccurred())

			// ========================================
			// PHASE 5: Verify created nodepools appear in all listings
			// ========================================

			By("listing nodepools by cluster - should find 2 created nodepools")
			clusterPager = npClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			foundNodePools := make(map[string]struct{})
			const createdNodePoolsCount = 2
			for clusterPager.More() {
				nodePoolList, err := clusterPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil {
						foundNodePools[*np.Name] = struct{}{}
					}
				}
			}
			Expect(len(foundNodePools)).To(Equal(createdNodePoolsCount), "Expected to find %d created nodepools in the list", createdNodePoolsCount)
			Expect(foundNodePools).To(HaveKey(nodePoolName1), "Expected to find nodepool %s in the list", nodePoolName1)
			Expect(foundNodePools).To(HaveKey(nodePoolName2), "Expected to find nodepool %s in the list", nodePoolName2)

			By("listing nodepools by resource group - should find 2 created nodepools")
			resourcesGroupPager = resourcesClient.NewListByResourceGroupPager(*resourceGroup.Name, &armresources.ClientListByResourceGroupOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			foundNodePools = make(map[string]struct{})
			for resourcesGroupPager.More() {
				nodePoolList, err := resourcesGroupPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil {
						foundNodePools[*np.Name] = struct{}{}
					}
				}
			}
			Expect(len(foundNodePools)).To(Equal(createdNodePoolsCount), "Expected to find %d created nodepools in the list", createdNodePoolsCount)
			// Nodepool name is in the format: "clustername/nodepoolname"
			Expect(foundNodePools).To(HaveKey(clusterName+"/"+nodePoolName1), "Expected to find nodepool %s in the list", clusterName+"/"+nodePoolName1)
			Expect(foundNodePools).To(HaveKey(clusterName+"/"+nodePoolName2), "Expected to find nodepool %s in the list", clusterName+"/"+nodePoolName2)

			By("listing nodepools by subscription - should find 2 created nodepools")
			subscriptionPager = resourcesClient.NewListPager(&armresources.ClientListOptions{
				Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
			})
			foundNodePools = make(map[string]struct{})
			for subscriptionPager.More() {
				nodePoolList, err := subscriptionPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil {
						foundNodePools[*np.Name] = struct{}{}
					}
				}
			}
			// Nodepool name is in the format: "clustername/nodepoolname"
			Expect(foundNodePools).To(HaveKey(clusterName+"/"+nodePoolName1), "Expected to find nodepool %s in the list", clusterName+"/"+nodePoolName1)
			Expect(foundNodePools).To(HaveKey(clusterName+"/"+nodePoolName2), "Expected to find nodepool %s in the list", clusterName+"/"+nodePoolName2)

			// ========================================
			// PHASE 6: Delete second nodepool
			// ========================================

			By("deleting second nodepool")
			Expect(framework.DeleteNodePool(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName2,
				25*time.Minute,
			)).To(Succeed())

			By("verifying second nodepool is deleted")
			_, getErr := framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName2,
			)
			Expect(getErr).To(HaveOccurred())

			// ========================================
			// PHASE 7: Verify deleted nodepool does not appear in all listings
			// ========================================

			By("listing nodepools by cluster - should find first created nodepool and not the deleted one")
			clusterPager = npClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			nodePoolCount = 0
			foundCreatedNodePool = false
			for clusterPager.More() {
				nodePoolList, err := clusterPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				nodePoolCount += len(nodePoolList.Value)
				for _, np := range nodePoolList.Value {
					Expect(*np.ID).ToNot(BeEmpty())
					if np.Name != nil && *np.Name == nodePoolName1 {
						foundCreatedNodePool = true
					}
				}
			}
			Expect(nodePoolCount).To(Equal(1), "Expected one listed nodepool")
			Expect(foundCreatedNodePool).To(BeTrue(), "Expected to find nodepool %s in the list", nodePoolName1)

			By("listing nodepools by resource group - should find first created nodepool and not the deleted one")
			// Eventually is used here because Azure's generic resource listing API has eventual consistency.
			// After deleting a nodepool, the deletion is immediate in the service, but the generic ARM
			// resource listing API may still return the deleted resource for a short time (typically seconds
			// to minutes) until its cache updates. We retry every 5 seconds for up to 2 minutes to allow
			// the API to reflect the actual state.
			Eventually(func(g Gomega) {
				resourcesGroupPager := resourcesClient.NewListByResourceGroupPager(*resourceGroup.Name, &armresources.ClientListByResourceGroupOptions{
					Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
				})
				nodePoolCount := 0
				foundCreatedNodePool := false
				for resourcesGroupPager.More() {
					nodePoolList, err := resourcesGroupPager.NextPage(ctx)
					Expect(err).NotTo(HaveOccurred())
					nodePoolCount += len(nodePoolList.Value)
					for _, np := range nodePoolList.Value {
						Expect(*np.ID).ToNot(BeEmpty())
						// Nodepool name is in the format: "clustername/nodepoolname"
						if np.Name != nil && *np.Name == clusterName+"/"+nodePoolName1 {
							foundCreatedNodePool = true
						}
					}
				}
				g.Expect(nodePoolCount).To(Equal(1), "Expected one listed nodepool")
				g.Expect(foundCreatedNodePool).To(BeTrue(), "Expected to find nodepool %s in the list", nodePoolName1)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("listing nodepools by subscription - should find first created nodepool and not the deleted one")
			// Eventually is used here because Azure's generic resource listing API has eventual consistency.
			// After deleting a nodepool, the deletion is immediate in the service, but the generic ARM
			// resource listing API may still return the deleted resource for a short time (typically seconds
			// to minutes) until its cache updates. We retry every 5 seconds for up to 2 minutes to allow
			// the API to reflect the actual state.
			Eventually(func(g Gomega) {
				subscriptionPager := resourcesClient.NewListPager(&armresources.ClientListOptions{
					Filter: ptr.To("resourceType eq 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools'"),
				})
				foundNodePools := make(map[string]struct{})
				for subscriptionPager.More() {
					nodePoolList, err := subscriptionPager.NextPage(ctx)
					Expect(err).NotTo(HaveOccurred())
					for _, np := range nodePoolList.Value {
						Expect(*np.ID).ToNot(BeEmpty())
						if np.Name != nil {
							foundNodePools[*np.Name] = struct{}{}
						}
					}
				}
				// Nodepool name is in the format: "clustername/nodepoolname"
				g.Expect(foundNodePools).To(HaveKey(clusterName+"/"+nodePoolName1), "Expected to find nodepool %s in the list", clusterName+"/"+nodePoolName1)
				g.Expect(foundNodePools).ToNot(HaveKey(clusterName+"/"+nodePoolName2), "Expected not to find deleted nodepool %s in the list", clusterName+"/"+nodePoolName2)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		},
	)
})
