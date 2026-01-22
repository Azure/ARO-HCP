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
)

var _ = Describe("Customer", func() {

	It("should not be able to reuse subnets and NSGs between clusters",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 3, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-nsg-subnet-reuse", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating customer resources")
			clusterParams1 := framework.NewDefaultClusterParams()
			clusterParams1.ClusterName = "basic-cluster"
			managedResourceGroupName1 := framework.SuffixName(*resourceGroup.Name, "-managed-1", 64)
			clusterParams1.ManagedResourceGroupName = managedResourceGroupName1

			clusterParams1, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams1,
				map[string]any{
					"customerNsgName":        customerNetworkSecurityGroupName + "1",
					"customerVnetName":       customerVnetName + "1",
					"customerVnetSubnetName": customerVnetSubnetName + "1",
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams1,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating customer resources with the same subnet resource ID")
			clusterParams2 := framework.NewDefaultClusterParams()
			clusterParams2.ClusterName = "cluster-subnet-reuse"
			managedResourceGroupName2 := framework.SuffixName(*resourceGroup.Name, "-managed-2", 64)
			clusterParams2.ManagedResourceGroupName = managedResourceGroupName2

			clusterParams2, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams2,
				map[string]any{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName + "2",
					"customerVnetName":       customerVnetName + "2",
					"customerVnetSubnetName": customerVnetSubnetName + "2",
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			clusterParams2.SubnetResourceID = clusterParams1.SubnetResourceID

			By("attempting to create HCP cluster with already used subnet resource")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams2,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred())
			GinkgoLogr.Error(err, "cluster deployment error")
			Expect(err.Error()).To(MatchRegexp("Subnet .* is already in use by another cluster"))

			By("creating customer resources with the same NSG resource ID")
			clusterParams3 := framework.NewDefaultClusterParams()
			clusterParams3.ClusterName = "cluster-nsg-reuse"
			managedResourceGroupName3 := framework.SuffixName(*resourceGroup.Name, "-managed-3", 64)
			clusterParams3.ManagedResourceGroupName = managedResourceGroupName3

			clusterParams3, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams3,
				map[string]any{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName + "3",
					"customerVnetName":       customerVnetName + "3",
					"customerVnetSubnetName": customerVnetSubnetName + "3",
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			clusterParams3.NsgResourceID = clusterParams1.NsgResourceID

			By("attempting to create HCP cluster with already used NSG resource")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams3,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred())
			GinkgoLogr.Error(err, "cluster deployment error")
			Expect(err.Error()).To(MatchRegexp("Network Security Group .* is already in use by another cluster"))

		})
})
