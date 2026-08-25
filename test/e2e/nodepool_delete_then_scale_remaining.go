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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// DEMO: scoped down to reproduce reports of scaling failures after a sibling nodepool is deleted.
// This test is not intended to merge.
var _ = Describe("Customer", func() {
	It("should be able to scale a remaining nodepool after a sibling nodepool is deleted",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "np-del-scale-hcp-cluster"
				firstNodePoolName   = "np-first"
				secondNodePoolName  = "np-second"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-del-scale", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group nodepool-del-scale")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			By("creating both single-node node pools in parallel")
			firstNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			firstNodePoolParams.NodePoolName = firstNodePoolName
			firstNodePoolParams.Replicas = int32(1)

			secondNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			secondNodePoolParams.NodePoolName = secondNodePoolName
			secondNodePoolParams.Replicas = int32(1)

			errCh := make(chan error, 2)
			group, groupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range []framework.NodePoolParams20240610{firstNodePoolParams, secondNodePoolParams} {
				group.Go(func() error {
					createErr := tc.CreateNodePoolFromParam20240610(
						groupCtx,
						GinkgoLogr,
						*resourceGroup.Name,
						managedResourceGroupName,
						customerClusterName,
						nodePoolParams,
						framework.NodePoolCreationTimeout,
					)
					if createErr != nil {
						errCh <- createErr
					}
					return createErr
				})
			}
			_ = group.Wait()
			close(errCh)
			var creationErrors []error
			for createErr := range errCh {
				creationErrors = append(creationErrors, createErr)
			}
			Expect(creationErrors).To(BeEmpty(), "nodepool creation errors: %v", creationErrors)

			By("verifying both node pools came up healthy")
			totalNodeCount := 2
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify initial node count of %d", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after initial creation")

			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("deleting the first node pool")
			Expect(framework.DeleteNodePool20240610(
				ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				firstNodePoolName,
				framework.NodePoolDeletionTimeout,
			)).To(Succeed(), "failed to delete node pool %s", firstNodePoolName)

			By("scaling the remaining node pool from 1 to 2 replicas")
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(2)),
				},
			}
			scaleResp, err := framework.UpdateNodePoolAndWait20240610(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				secondNodePoolName,
				update,
				framework.NodePoolScalingTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to scale node pool %s from 1 to 2 replicas after deleting sibling node pool %s", secondNodePoolName, firstNodePoolName)
			Expect(scaleResp.Properties).NotTo(BeNil(), "scale response Properties was nil")
			Expect(scaleResp.Properties.Replicas).NotTo(BeNil(), "scale response Properties.Replicas was nil")
			Expect(*scaleResp.Properties.Replicas).To(Equal(int32(2)), "expected scale response replicas to equal 2")

			By("verifying the remaining node pool has 2 ready nodes")
			Expect(verifiers.VerifyNodeCount(customerClusterName, 2).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of 2 after scale up")
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after scale up")
		})
})
