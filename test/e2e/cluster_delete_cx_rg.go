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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster then delete it by deleting the customer resource group",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg"
				customerVnetName                 = "customer-vnet"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "cx-rg-hcp-cluster"
				customerNodePool1Name            = "np-1"
				customerNodePool2Name            = "np-2"
				customerNodePool2VMSize          = "Standard_D4s_v3"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating the customer resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cx-rg-cluster", tc.Location())
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
				map[string]interface{}{
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

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating the first nodepool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePool1Name
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the second nodepool")
			nodePoolParams2 := framework.NewDefaultNodePoolParams()
			nodePoolParams2.ClusterName = customerClusterName
			nodePoolParams2.NodePoolName = customerNodePool2Name
			nodePoolParams2.VMSize = customerNodePool2VMSize
			nodePoolParams2.Replicas = int32(1)
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams2,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("deleting customer resource group to trigger cluster deletion")
			rgClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()
			networkClient, err := tc.GetARMNetworkClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred())
			err = framework.DeleteResourceGroup(ctx, rgClient, networkClient, *resourceGroup.Name, false, 60*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying customer resource group is deleted (404)")
			_, err = rgClient.Get(ctx, *resourceGroup.Name, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ResourceGroupNotFound"))

			By("verifying managed resource group is deleted (404)")
			_, err = rgClient.Get(ctx, managedResourceGroupName, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ResourceGroupNotFound"))
		})
})
