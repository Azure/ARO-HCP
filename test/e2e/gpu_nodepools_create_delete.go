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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("HCP Nodepools GPU instances", func() {
	It("creates and deletes a GPU nodepool in a single cluster",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.IntegrationOnly,
		func(ctx context.Context) {
			const (
				customerClusterName = "cluster-gpu-np"
				defaultNodePoolName = "np-1"
				gpuNodePoolName     = "gpu-np-1"
			)

			tc := framework.NewTestContext()

			By("discovering an available GPU VM size")
			gpuVMSize, err := tc.SelectVMSize(ctx, framework.GPUNodePoolVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to discover a GPU VM size")

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-gpu-nodepool", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for GPU nodepool test")

			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for GPU nodepool cluster")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for GPU nodepool test")

			By("getting credentials and verifying cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260630(
				ctx,
				tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(), "failed to verify basic cluster health for %s", customerClusterName)

			// this test deletes gpu node pool later. if we only create gpu node pool and then delete it,
			// we will get an error: "The last node pool can not be deleted from a cluster."
			// that's why firstly we create a default node pool (which by the way is cheaper than gpu node pool)
			// so that gpu node pool can be deleted later without any error.
			// we create a default node pool with two replicas instead of one,
			// because in the latter case we will get this error: "A hosted cluster requires at least 2 replicas"
			By("creating default nodepool")
			defaultNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			defaultNodePoolParams.ClusterName = customerClusterName
			defaultNodePoolParams.NodePoolName = defaultNodePoolName
			defaultNodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				defaultNodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create default nodepool %s", defaultNodePoolName)

			By(fmt.Sprintf("creating GPU nodepool with VM size %q", gpuVMSize))
			gpuNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			gpuNodePoolParams.ClusterName = customerClusterName
			gpuNodePoolParams.NodePoolName = gpuNodePoolName
			gpuNodePoolParams.Replicas = int32(1)
			gpuNodePoolParams.VMSize = gpuVMSize

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				gpuNodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create GPU nodepool %s with VM size %s", gpuNodePoolName, gpuVMSize)

			By("verifying GPU nodepool provisioning succeeded with correct VM size")
			created, err := framework.GetNodePool20240610(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				gpuNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get GPU nodepool %s", gpuNodePoolName)
			Expect(created.Properties).ToNot(BeNil(), "GPU nodepool Properties was nil")
			Expect(created.Properties.ProvisioningState).ToNot(BeNil(), "GPU nodepool Properties.ProvisioningState was nil")
			Expect(*created.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "GPU nodepool %s provisioning state should be Succeeded", gpuNodePoolName)
			Expect(created.Properties.Platform).ToNot(BeNil(), "GPU nodepool Properties.Platform was nil")
			Expect(created.Properties.Platform.VMSize).ToNot(BeNil(), "GPU nodepool Properties.Platform.VMSize was nil")
			Expect(*created.Properties.Platform.VMSize).To(Equal(gpuVMSize), "GPU nodepool %s VM size should be %s", gpuNodePoolName, gpuVMSize)

			By("deleting GPU nodepool")
			Expect(framework.DeleteNodePool20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				gpuNodePoolName,
				framework.NodePoolDeletionTimeout,
			)).To(Succeed(), "failed to delete GPU nodepool %s", gpuNodePoolName)

			By("confirming GPU nodepool has been deleted")
			_, getErr := framework.GetNodePool20240610(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				gpuNodePoolName,
			)
			Expect(getErr).To(HaveOccurred(), "expected GPU nodepool %s to be deleted but it still exists", gpuNodePoolName)
		},
	)
})
