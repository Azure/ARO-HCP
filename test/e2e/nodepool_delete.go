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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to delete a nodepool",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName   = "np-delete-hcp-cluster"
				customerNodePoolName  = "np-delete-main"
				deletableNodePoolName = "np-deletable"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-delete", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group nodepool-delete")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			// This test needs to create two node pools and delete one because
			// a cluster can't be left without a node pool i.e you can't delete the last nodepool.
			// Once https://redhat.atlassian.net/browse/ARO-26333 is implemented,
			// the test should be able to just create a single node pool and delete it.
			By("creating two node pools in parallel")
			mainNodeCount := 2
			deletableNodeCount := 2

			mainNodePoolParams := framework.NewDefaultNodePoolParams20260901()
			mainNodePoolParams.NodePoolName = customerNodePoolName
			mainNodePoolParams.Replicas = int32(mainNodeCount)

			deletableNodePoolParams := framework.NewDefaultNodePoolParams20260901()
			deletableNodePoolParams.NodePoolName = deletableNodePoolName
			deletableNodePoolParams.Replicas = int32(deletableNodeCount)

			errCh := make(chan error, 2)
			group, groupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range []framework.NodePoolParams20260901{mainNodePoolParams, deletableNodePoolParams} {
				group.Go(func() error {
					createErr := tc.CreateNodePoolFromParam20260901(
						groupCtx,
						GinkgoLogr,
						*resourceGroup.Name,
						clusterParams.ManagedResourceGroupName,
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
			Expect(group.Wait()).To(Succeed(), "failed to create node pools")
			close(errCh)
			var creationErrors []error
			for createErr := range errCh {
				creationErrors = append(creationErrors, createErr)
			}
			Expect(creationErrors).To(BeEmpty(), "nodepool creation errors: %v", creationErrors)

			By("verifying initial nodes count and ready status")
			totalNodeCount := mainNodeCount + deletableNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify initial node count of %d", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after initial creation")

			nodePoolsClient := tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("deleting the deletable nodepool via ARM")
			poller, err := nodePoolsClient.BeginDelete(ctx, *resourceGroup.Name, customerClusterName, deletableNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start deletion of node pool %s", deletableNodePoolName)

			_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
				Frequency: framework.StandardPollInterval,
			})
			Expect(err).NotTo(HaveOccurred(), "failed to complete deletion of node pool %s", deletableNodePoolName)

			By("verifying nodes count and ready status after nodepool deletion")
			totalNodeCount = mainNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after deleting nodepool", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after nodepool deletion")

			By("verifying all remaining nodes belong to the main nodepool")
			Expect(verifiers.VerifyAllNodesFromNodePool(customerNodePoolName).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes belong to nodepool %s", customerNodePoolName)

			By("verifying the deleted nodepool no longer exists")
			_, err = nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, deletableNodePoolName, nil)
			Expect(err).To(HaveOccurred(), "expected error when getting deleted nodepool %s", deletableNodePoolName)
		})
})
