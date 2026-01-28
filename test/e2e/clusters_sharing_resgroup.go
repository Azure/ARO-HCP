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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create several HCP clusters in their customer resource group, but not in the same managed resource group",
		labels.RequireNothing,
		labels.AroRpApiCompatible,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"

				customerNetworkSecurityGroupName2 = "customer-nsg2-name"
				customerVnetName2                 = "customer-vnet2-name"
				customerVnetSubnetName2           = "customer-vnet-subnet2"
				customerClusterName2              = "basic-hcp-cluster2"

				customerNetworkSecurityGroupName3 = "customer-nsg3-name"
				customerVnetName3                 = "customer-vnet3-name"
				customerVnetSubnetName3           = "customer-vnet-subnet3"
				customerClusterName3              = "basic-hcp-cluster3"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 3, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a shared customer resource group")
			customerResourceGroup, err := tc.NewResourceGroup(ctx, "customer-rg", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating customer infrastructure and managed identities for first cluster")
			clusterParams1 := framework.NewDefaultClusterParams()
			clusterParams1.ClusterName = customerClusterName
			clusterParams1.ManagedResourceGroupName = framework.SuffixName(*customerResourceGroup.Name, "-managed", 64)
			clusterParams1, err = tc.CreateClusterCustomerResources(ctx, customerResourceGroup, clusterParams1,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating customer infrastructure and managed identities for second cluster")
			clusterParams2 := framework.NewDefaultClusterParams()
			clusterParams2.ClusterName = customerClusterName2
			clusterParams2.ManagedResourceGroupName = framework.SuffixName(*customerResourceGroup.Name, "-managed-2", 64)
			clusterParams2, err = tc.CreateClusterCustomerResources(ctx, customerResourceGroup, clusterParams2,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName2,
					"customerVnetName":       customerVnetName2,
					"customerVnetSubnetName": customerVnetSubnetName2,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("starting creation of both clusters in parallel")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			// Start first cluster creation
			poller1, err := framework.BeginCreateHCPCluster(
				ctx,
				GinkgoLogr,
				clusterClient,
				*customerResourceGroup.Name,
				clusterParams1.ClusterName,
				clusterParams1,
				tc.Location(),
			)
			Expect(err).NotTo(HaveOccurred())

			// Start second cluster creation
			poller2, err := framework.BeginCreateHCPCluster(
				ctx,
				GinkgoLogr,
				clusterClient,
				*customerResourceGroup.Name,
				clusterParams2.ClusterName,
				clusterParams2,
				tc.Location(),
			)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for first cluster to complete creation")
			_, err = poller1.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
				Frequency: framework.StandardPollInterval,
			})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for second cluster to complete creation")
			_, err = poller2.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
				Frequency: framework.StandardPollInterval,
			})
			Expect(err).NotTo(HaveOccurred())

			// Third cluster (should fail)
			By("creating customer infrastructure and managed identities for third cluster")
			clusterParams3 := framework.NewDefaultClusterParams()
			clusterParams3.ClusterName = customerClusterName3
			clusterParams3.ManagedResourceGroupName = clusterParams2.ManagedResourceGroupName // Reuse cluster2's managed RG
			clusterParams3, err = tc.CreateClusterCustomerResources(ctx, customerResourceGroup, clusterParams3,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName3,
					"customerVnetName":       customerVnetName3,
					"customerVnetSubnetName": customerVnetSubnetName3,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create a third cluster using the same managed resource group as the second cluster")
			err = tc.CreateHCPClusterFromParam(ctx, GinkgoLogr, *customerResourceGroup.Name, clusterParams3, 45*time.Minute)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(MatchRegexp("please provide a unique managed resource group name")))

			By("verifying that the managed resource group still exists")
			_, err = tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient().Get(ctx, clusterParams2.ManagedResourceGroupName, nil)
			Expect(err).NotTo(HaveOccurred())
		})
})
