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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to update nodepool replicas and autoscaling as well as list node pools by cluster and resource group",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName = "hcp-cluster-update-nodes"

				scaleDownNodePoolName = "np-scale-down"
				scaleUpNodePoolName   = "np-scale-up"
				autoscaleNodePoolName = "np-autoscale"

				deployedNodePoolsNumber = 3

				scaleDownNodePoolInitialReplicas = 2
				scaleDownNodePoolUpdatedReplicas = 1

				scaleUpNodePoolInitialReplicas = 1
				scaleUpNodePoolUpdatedReplicas = 2

				autoscaleNodePoolInitialReplicas = 1
				autoscaleNodePoolUpdatedReplicas = 0
				autoscaleNodePoolMinReplicas     = 1
				autoscaleNodePoolMaxReplicas     = 2

				initialNodeCount = scaleDownNodePoolInitialReplicas + scaleUpNodePoolInitialReplicas + autoscaleNodePoolInitialReplicas
				finalNodeCount   = scaleDownNodePoolUpdatedReplicas + scaleUpNodePoolUpdatedReplicas + autoscaleNodePoolMinReplicas
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			verifyNodePoolListByCluster := func(ctx context.Context, rgName, clusterName string, expectedNodePoolNames []string) {
				npClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
				pager := npClient.NewListByParentPager(rgName, clusterName, nil)

				var listedNodePoolNames []string
				for pager.More() {
					page, err := pager.NextPage(ctx)
					Expect(err).NotTo(HaveOccurred(), "failed to list node pools by cluster %s", clusterName)
					for _, np := range page.Value {
						Expect(np.ID).NotTo(BeNil(), "listed node pool ID was nil in cluster %s", clusterName)
						Expect(*np.ID).NotTo(BeEmpty(), "listed node pool ID was empty in cluster %s", clusterName)
						if np.Name != nil {
							listedNodePoolNames = append(listedNodePoolNames, *np.Name)
						}
					}
				}

				Expect(listedNodePoolNames).To(ConsistOf(expectedNodePoolNames), "expected %v node pools listed by cluster %s, got %v", expectedNodePoolNames, clusterName, listedNodePoolNames)
			}

			verifyNodePoolListByRG := func(ctx context.Context, rgName string, expectedNodePoolNames []string) {
				clientFactory := tc.Get20240610ClientFactoryOrDie(ctx)
				hcpClient := clientFactory.NewHcpOpenShiftClustersClient()
				npClient := clientFactory.NewNodePoolsClient()

				clusterPager := hcpClient.NewListByResourceGroupPager(rgName, nil)

				var listedNodePoolNames []string
				for clusterPager.More() {
					clusterPage, err := clusterPager.NextPage(ctx)
					Expect(err).NotTo(HaveOccurred(), "failed to list clusters in resource group %s", rgName)
					for _, cluster := range clusterPage.Value {
						if cluster.Name == nil {
							continue
						}
						npPager := npClient.NewListByParentPager(rgName, *cluster.Name, nil)
						for npPager.More() {
							npPage, err := npPager.NextPage(ctx)
							Expect(err).NotTo(HaveOccurred(), "failed to list node pools for cluster %s in resource group %s", *cluster.Name, rgName)
							for _, np := range npPage.Value {
								Expect(np.ID).NotTo(BeNil(), "listed node pool ID was nil in cluster %s", *cluster.Name)
								Expect(*np.ID).NotTo(BeEmpty(), "listed node pool ID was empty in cluster %s", *cluster.Name)
								if np.Name != nil {
									listedNodePoolNames = append(listedNodePoolNames, *np.Name)
								}
							}
						}
					}
				}

				Expect(listedNodePoolNames).To(ConsistOf(expectedNodePoolNames), "expected %v node pools listed by resource group %s, got %v", expectedNodePoolNames, rgName, listedNodePoolNames)
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-update-nodes", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("listing node pools by empty resource group")
			verifyNodePoolListByRG(ctx, *resourceGroup.Name, []string{})

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("listing node pools by cluster without node pools")
			verifyNodePoolListByCluster(ctx, *resourceGroup.Name, customerClusterName, []string{})

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating %d node pools in parallel", deployedNodePoolsNumber))
			scaleDownParams := framework.NewDefaultNodePoolParams()
			scaleDownParams.NodePoolName = scaleDownNodePoolName
			scaleDownParams.Replicas = scaleDownNodePoolInitialReplicas

			scaleUpParams := framework.NewDefaultNodePoolParams()
			scaleUpParams.NodePoolName = scaleUpNodePoolName
			scaleUpParams.Replicas = scaleUpNodePoolInitialReplicas

			autoscaleParams := framework.NewDefaultNodePoolParams()
			autoscaleParams.NodePoolName = autoscaleNodePoolName
			autoscaleParams.Replicas = autoscaleNodePoolInitialReplicas

			allNodePoolParams := []framework.NodePoolParams{scaleDownParams, scaleUpParams, autoscaleParams}
			nodePoolCreateErrCh := make(chan error, deployedNodePoolsNumber)
			nodePoolCreateGroup, nodePoolCreateGroupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range allNodePoolParams {
				nodePoolCreateGroup.Go(func() error {
					createErr := tc.CreateNodePoolFromParam(
						nodePoolCreateGroupCtx,
						*resourceGroup.Name,
						customerClusterName,
						nodePoolParams,
						45*time.Minute,
					)
					if createErr != nil {
						nodePoolCreateErrCh <- createErr
					}
					return createErr
				})
			}
			_ = nodePoolCreateGroup.Wait()
			close(nodePoolCreateErrCh)
			var creationErrors []error
			for createErr := range nodePoolCreateErrCh {
				creationErrors = append(creationErrors, createErr)
			}
			Expect(creationErrors).To(BeEmpty(), "nodepool creation errors: %v", creationErrors)

			By("verifying nodes count and status after initial creation")
			Expect(verifiers.VerifyNodeCount(initialNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			deployedNodePoolNames := []string{scaleDownNodePoolName, scaleUpNodePoolName, autoscaleNodePoolName}
			By(fmt.Sprintf("listing node pools by cluster after creating %d node pools", deployedNodePoolsNumber))
			verifyNodePoolListByCluster(ctx, *resourceGroup.Name, customerClusterName, deployedNodePoolNames)

			By(fmt.Sprintf("listing node pools by resource group after creating %d node pools", deployedNodePoolsNumber))
			verifyNodePoolListByRG(ctx, *resourceGroup.Name, deployedNodePoolNames)

			By("scaling down, scaling up, and enabling autoscaling in parallel")
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			var scaleDownNodePoolResp, scaleUpNodePoolResp, autoscaleNodePoolResp *hcpsdk20240610preview.NodePool

			nodePoolUpdateErrCh := make(chan error, deployedNodePoolsNumber)
			nodePoolUpdateGroup, nodePoolUpdateGroupCtx := errgroup.WithContext(ctx)

			// scale down
			nodePoolUpdateGroup.Go(func() error {
				var updateErr error
				update := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(scaleDownNodePoolUpdatedReplicas)),
					},
				}
				scaleDownNodePoolResp, updateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					scaleDownNodePoolName,
					update,
					20*time.Minute,
				)
				if updateErr != nil {
					nodePoolUpdateErrCh <- updateErr
				}
				return updateErr
			})

			// scale up
			nodePoolUpdateGroup.Go(func() error {
				var updateErr error
				update := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(scaleUpNodePoolUpdatedReplicas)),
					},
				}
				scaleUpNodePoolResp, updateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					scaleUpNodePoolName,
					update,
					20*time.Minute,
				)
				if updateErr != nil {
					nodePoolUpdateErrCh <- updateErr
				}
				return updateErr
			})

			// enable autoscaling
			nodePoolUpdateGroup.Go(func() error {
				var updateErr error
				update := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(autoscaleNodePoolUpdatedReplicas)),
						AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
							Min: to.Ptr(int32(autoscaleNodePoolMinReplicas)),
							Max: to.Ptr(int32(autoscaleNodePoolMaxReplicas)),
						},
					},
				}
				autoscaleNodePoolResp, updateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					autoscaleNodePoolName,
					update,
					20*time.Minute,
				)
				if updateErr != nil {
					nodePoolUpdateErrCh <- updateErr
				}
				return updateErr
			})

			_ = nodePoolUpdateGroup.Wait()
			close(nodePoolUpdateErrCh)
			var nodePoolUpdateErrors []error
			for nodePoolUpdateErr := range nodePoolUpdateErrCh {
				nodePoolUpdateErrors = append(nodePoolUpdateErrors, nodePoolUpdateErr)
			}
			Expect(nodePoolUpdateErrors).To(BeEmpty(), "nodepool update errors: %v", nodePoolUpdateErrors)

			By("verifying scaling down nodepool state after update")
			Expect(scaleDownNodePoolResp).NotTo(BeNil(), "scale down response was nil")
			Expect(scaleDownNodePoolResp.Properties).NotTo(BeNil(), "scale down response Properties was nil")
			Expect(scaleDownNodePoolResp.Properties.Replicas).NotTo(BeNil(), "scale down response Properties.Replicas was nil")
			Expect(*scaleDownNodePoolResp.Properties.Replicas).To(Equal(int32(scaleDownNodePoolUpdatedReplicas)))

			By("verifying scaling up nodepool state after update")
			Expect(scaleUpNodePoolResp).NotTo(BeNil(), "scale up response was nil")
			Expect(scaleUpNodePoolResp.Properties).NotTo(BeNil(), "scale up response Properties was nil")
			Expect(scaleUpNodePoolResp.Properties.Replicas).NotTo(BeNil(), "scale up response Properties.Replicas was nil")
			Expect(*scaleUpNodePoolResp.Properties.Replicas).To(Equal(int32(scaleUpNodePoolUpdatedReplicas)))

			By("verifying autoscaling nodepool state after update")
			Expect(autoscaleNodePoolResp).NotTo(BeNil(), "autoscale nodepool response was nil")
			Expect(autoscaleNodePoolResp.Properties).NotTo(BeNil(), "autoscale nodepool response Properties was nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling).NotTo(BeNil(), "autoscale nodepool response Properties.AutoScaling was nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling.Min).NotTo(BeNil(), "autoscale nodepool response Properties.AutoScaling.Min was nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling.Max).NotTo(BeNil(), "autoscale nodepool response Properties.AutoScaling.Max was nil")
			Expect(*autoscaleNodePoolResp.Properties.AutoScaling.Min).To(Equal(int32(autoscaleNodePoolMinReplicas)))
			Expect(*autoscaleNodePoolResp.Properties.AutoScaling.Max).To(Equal(int32(autoscaleNodePoolMaxReplicas)))

			By("verifying nodes count and status after all updates")
			Expect(verifiers.VerifyNodeCount(finalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("listing node pools by cluster after node pools updates")
			verifyNodePoolListByCluster(ctx, *resourceGroup.Name, customerClusterName, deployedNodePoolNames)

			By("listing node pools by resource group after node pools updates")
			verifyNodePoolListByRG(ctx, *resourceGroup.Name, deployedNodePoolNames)
		})
})
