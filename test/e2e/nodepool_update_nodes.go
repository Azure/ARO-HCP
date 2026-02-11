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
	It("should be able to update nodepool replicas and autoscaling",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-update-nodes-hcp-cluster"
				customerNodePoolName = "np-update-nodes"
				oneNodePoolName      = "np-one-node"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-update-nodes", tc.Location())
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

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pools in parallel")
			mainNodeCount := 2
			oneNodeCount := 1

			mainNodePoolParams := framework.NewDefaultNodePoolParams()
			mainNodePoolParams.NodePoolName = customerNodePoolName
			mainNodePoolParams.Replicas = int32(mainNodeCount)

			oneNodePoolParams := framework.NewDefaultNodePoolParams()
			oneNodePoolParams.NodePoolName = oneNodePoolName
			oneNodePoolParams.Replicas = int32(oneNodeCount)

			errCh := make(chan error, 2)
			group, groupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range []framework.NodePoolParams{mainNodePoolParams, oneNodePoolParams} {
				group.Go(func() error {
					createErr := tc.CreateNodePoolFromParam(
						groupCtx,
						*resourceGroup.Name,
						customerClusterName,
						nodePoolParams,
						45*time.Minute,
					)
					if createErr != nil {
						errCh <- createErr
					}
					return createErr
				})
			}
			err = group.Wait()
			close(errCh)
			var creationErrors []error
			for createErr := range errCh {
				creationErrors = append(creationErrors, createErr)
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(creationErrors).To(BeEmpty(), "nodepool creation errors: %v", creationErrors)

			By("verifying nodes count and status after initial creation")
			totalNodeCount := mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("scaling up the nodepool replicas from 2 to 3 replicas")
			mainNodeCount = 3
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleUpResp, err := framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				20*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(scaleUpResp.Properties).NotTo(BeNil())
			Expect(scaleUpResp.Properties.Replicas).NotTo(BeNil())
			Expect(*scaleUpResp.Properties.Replicas).To(Equal(int32(mainNodeCount)))

			By("verifying nodes count and status after scaling up")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("scaling down the nodepool replicas from 3 to 2 replicas")
			mainNodeCount = 2
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleDownResp, err := framework.UpdateNodePoolAndWait(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				20*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(scaleDownResp.Properties).NotTo(BeNil())
			Expect(scaleDownResp.Properties.Replicas).NotTo(BeNil())
			Expect(*scaleDownResp.Properties.Replicas).To(Equal(int32(mainNodeCount)))

			By("verifying nodes count and status after scaling down")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("updating the one-replica nodepool replicas to 0 and enabling autoscaling with a PATCH")
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(0)),
					AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
						Min: to.Ptr(int32(2)),
						Max: to.Ptr(int32(3)),
					},
				},
			}
			autoscaleResp, err := framework.UpdateNodePoolAndWait(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				oneNodePoolName,
				update,
				20*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(autoscaleResp.Properties).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling.Min).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling.Max).NotTo(BeNil())
			Expect(*autoscaleResp.Properties.AutoScaling.Min).To(Equal(int32(2)))
			Expect(*autoscaleResp.Properties.AutoScaling.Max).To(Equal(int32(3)))

			By("verifying nodes count and status after enabling autoscaling")
			oneNodeCount = 2
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())
		})
})
