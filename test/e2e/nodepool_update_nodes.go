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

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-update-nodes", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group nodepool-update-nodes")

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

			By("creating the node pools in parallel")
			mainNodeCount := 2
			oneNodeCount := 1

			mainNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			mainNodePoolParams.NodePoolName = customerNodePoolName
			mainNodePoolParams.Replicas = int32(mainNodeCount)

			oneNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			oneNodePoolParams.NodePoolName = oneNodePoolName
			oneNodePoolParams.Replicas = int32(oneNodeCount)

			errCh := make(chan error, 2)
			group, groupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range []framework.NodePoolParams20240610{mainNodePoolParams, oneNodePoolParams} {
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

			By("verifying nodes count and ready status")
			totalNodeCount := mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify initial node count of %d", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after initial creation")

			By("scaling up the nodepool replicas from 2 to 3 replicas")
			mainNodeCount = 3
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleUpResp, err := framework.UpdateNodePoolAndWait20240610(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				framework.NodePoolScalingTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to scale up node pool %s from 2 to 3 replicas", customerNodePoolName)
			Expect(scaleUpResp.Properties).NotTo(BeNil(), "scale up response Properties was nil")
			Expect(scaleUpResp.Properties.Replicas).NotTo(BeNil(), "scale up response Properties.Replicas was nil")
			Expect(*scaleUpResp.Properties.Replicas).To(Equal(int32(mainNodeCount)), "expected scale up response replicas to equal %d", mainNodeCount)

			By("verifying nodes count and ready status")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after scale up", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after scale up")

			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("scaling down the nodepool replicas from 3 to 2 replicas")
			mainNodeCount = 2
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleDownResp, err := framework.UpdateNodePoolAndWait20240610(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				framework.NodePoolScalingTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to scale down node pool %s from 3 to 2 replicas", customerNodePoolName)
			Expect(scaleDownResp.Properties).NotTo(BeNil(), "scale down response Properties was nil")
			Expect(scaleDownResp.Properties.Replicas).NotTo(BeNil(), "scale down response Properties.Replicas was nil")
			Expect(*scaleDownResp.Properties.Replicas).To(Equal(int32(mainNodeCount)), "expected scale down response replicas to equal %d", mainNodeCount)

			By("verifying nodes count and ready status")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after scale down", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after scale down")

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
			autoscaleResp, err := framework.UpdateNodePoolAndWait20240610(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				oneNodePoolName,
				update,
				framework.NodePoolScalingTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to enable autoscaling on node pool %s", oneNodePoolName)
			Expect(autoscaleResp.Properties).NotTo(BeNil(), "autoscale response Properties was nil")
			Expect(autoscaleResp.Properties.AutoScaling).NotTo(BeNil(), "autoscale response Properties.AutoScaling was nil")
			Expect(autoscaleResp.Properties.AutoScaling.Min).NotTo(BeNil(), "autoscale response Properties.AutoScaling.Min was nil")
			Expect(autoscaleResp.Properties.AutoScaling.Max).NotTo(BeNil(), "autoscale response Properties.AutoScaling.Max was nil")
			Expect(*autoscaleResp.Properties.AutoScaling.Min).To(Equal(int32(2)), "expected autoscale response min to equal 2")
			Expect(*autoscaleResp.Properties.AutoScaling.Max).To(Equal(int32(3)), "expected autoscale response max to equal 3")

			By("verifying nodes count and ready status")
			oneNodeCount = 2
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after enabling autoscaling", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after enabling autoscaling")
		})
})
