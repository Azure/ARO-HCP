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

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster and custom node pool osDisk size",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName             = "hcp-cluster-np-128"
				customerNodePoolName            = "nodepool-128GiB"
				customerNodeOsDiskSizeGiB int32 = 128
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			resourceGroup, err := tc.NewResourceGroup(ctx, "clusternp128", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for nodepool osDisk test")
			useOldAPI, err := tc.APIVersionAvailable(ctx, *resourceGroup.Name, metadataapi.APIVersionV20260630Preview)
			Expect(err).NotTo(HaveOccurred(), "failed to check whether v20260630preview is available")

			// creating cluster parameters
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			if useOldAPI {
				By("creating a resource group using 20260630preview API")
				clusterParams := framework.NewDefaultClusterParams20260630()
				clusterParams.ClusterName = customerClusterName
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				By("creating customer resources with v20260630preview")
				clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
				Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)
				By("creating the HCP cluster with v20260630preview")
				err = tc.CreateHCPClusterFromParam20260630(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			} else {
				By("creating a resource group using 20260901preview API")
				clusterParams := framework.NewDefaultClusterParams20260901()
				clusterParams.ClusterName = customerClusterName
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				By("creating customer resources with v20260901preview")
				clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
				Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)
				By("creating the HCP cluster with v20260901preview")
				err = tc.CreateHCPClusterFromParam20260901(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("creating the node pool with custom osDisk size")
			var getDiskSize func() (int32, string, error)
			if useOldAPI {
				nodePoolParams := framework.NewDefaultNodePoolParams20260630()
				nodePoolParams.ClusterName = customerClusterName
				nodePoolParams.NodePoolName = customerNodePoolName
				nodePoolParams.OSDiskSizeGiB = customerNodeOsDiskSizeGiB
				err = tc.CreateNodePoolFromParam20260630(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, customerClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
				getDiskSize = func() (int32, string, error) {
					pool, err := framework.GetNodePool20260630(ctx, tc.Get20260630ClientFactoryOrDie(ctx).NewNodePoolsClient(), *resourceGroup.Name, customerClusterName, customerNodePoolName)
					if err != nil {
						return 0, "", err
					}
					Expect(pool.Properties).NotTo(BeNil(), "v20260630preview nodepool Properties was nil")
					Expect(pool.Properties.ProvisioningState).NotTo(BeNil(), "v20260630preview nodepool provisioning state was nil")
					Expect(pool.Properties.Platform).NotTo(BeNil(), "v20260630preview nodepool Platform was nil")
					Expect(pool.Properties.Platform.OSDisk).NotTo(BeNil(), "v20260630preview nodepool OSDisk was nil")
					Expect(pool.Properties.Platform.OSDisk.SizeGiB).NotTo(BeNil(), "v20260630preview nodepool OS disk size was nil")
					return *pool.Properties.Platform.OSDisk.SizeGiB, string(*pool.Properties.ProvisioningState), nil
				}
			} else {
				nodePoolParams := framework.NewDefaultNodePoolParams20260901()
				nodePoolParams.ClusterName = customerClusterName
				nodePoolParams.NodePoolName = customerNodePoolName
				nodePoolParams.OSDiskSizeGiB = customerNodeOsDiskSizeGiB
				err = tc.CreateNodePoolFromParam20260901(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, customerClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
				getDiskSize = func() (int32, string, error) {
					pool, err := framework.GetNodePool20260901(ctx, tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient(), *resourceGroup.Name, customerClusterName, customerNodePoolName)
					if err != nil {
						return 0, "", err
					}
					Expect(pool.Properties).NotTo(BeNil(), "v20260901preview nodepool Properties was nil")
					Expect(pool.Properties.ProvisioningState).NotTo(BeNil(), "v20260901preview nodepool provisioning state was nil")
					Expect(pool.Properties.Platform).NotTo(BeNil(), "v20260901preview nodepool Platform was nil")
					Expect(pool.Properties.Platform.OSDisk).NotTo(BeNil(), "v20260901preview nodepool OSDisk was nil")
					Expect(pool.Properties.Platform.OSDisk.SizeGiB).NotTo(BeNil(), "v20260901preview nodepool OS disk size was nil")
					return *pool.Properties.Platform.OSDisk.SizeGiB, string(*pool.Properties.ProvisioningState), nil
				}
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q with 128GiB osDisk", customerNodePoolName)

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", customerClusterName)

			By("verifying the node pool is created and has the correct osDisk size")
			diskSizeGiB, provisioningState, err := getDiskSize()
			Expect(err).NotTo(HaveOccurred(), "failed to get node pool %q for cluster %q", customerNodePoolName, customerClusterName)
			Expect(provisioningState).To(Equal("Succeeded"), "nodepool %q provisioning state should be Succeeded", customerNodePoolName)
			Expect(diskSizeGiB).To(Equal(customerNodeOsDiskSizeGiB), "nodepool OS disk size should be %d GiB", customerNodeOsDiskSizeGiB)
		})
})
