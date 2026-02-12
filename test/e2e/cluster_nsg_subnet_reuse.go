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
				err := tc.AssignIdentityContainers(ctx, 2, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-nsg-subnet-reuse", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating customer resources")
			clusterParams1 := framework.NewDefaultClusterParams()
			clusterParams1.ClusterName = "basic-cluster"
			clusterParams1.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed-1", 64)

			clusterParams2 := framework.NewDefaultClusterParams()
			clusterParams2.ClusterName = "cluster-subnet-reuse"
			clusterParams2.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed-2", 64)

			var err1, err2 error

			customerRes1DoneCh := make(chan struct{}, 1)
			customerRes2DoneCh := make(chan struct{}, 1)

			go func() {
				defer close(customerRes1DoneCh)
				clusterParams1, err1 = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams1,
					map[string]any{
						"customerNsgName":        customerNetworkSecurityGroupName + "1",
						"customerVnetName":       customerVnetName + "1",
						"customerVnetSubnetName": customerVnetSubnetName + "1",
					},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
			}()
			go func() {
				defer close(customerRes2DoneCh)
				clusterParams2, err2 = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams2,
					map[string]any{
						"customerNsgName":        customerNetworkSecurityGroupName + "2",
						"customerVnetName":       customerVnetName + "2",
						"customerVnetSubnetName": customerVnetSubnetName + "2",
					},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
			}()

			<-customerRes1DoneCh
			Expect(err1).NotTo(HaveOccurred())

			By("creating HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams1,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			<-customerRes2DoneCh
			Expect(err2).NotTo(HaveOccurred())

			By("attempting to create HCP cluster with already used subnet resource")
			originalSubnetResourceID := clusterParams2.SubnetResourceID
			clusterParams2.SubnetResourceID = clusterParams1.SubnetResourceID

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
			clusterParams2.SubnetResourceID = originalSubnetResourceID

			By("attempting to create HCP cluster with already used NSG resource")

			clusterParams2.NsgResourceID = clusterParams1.NsgResourceID
			clusterParams2.ClusterName = "cluster-nsg-reuse"
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams2,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred())
			GinkgoLogr.Error(err, "cluster deployment error")
			Expect(err.Error()).To(MatchRegexp("Network Security Group .* is already in use by another cluster"))

		})
})
