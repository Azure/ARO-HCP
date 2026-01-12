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
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing. per-test initialization usually ages better than shared.
	})

	It("should be able to create node pools with ARM64-based VMs",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "arm64-vm-customer-nsg"
				customerVnetName                 = "arm64-vm-customer-vnet"
				customerVnetSubnetName           = "arm64-vm-subnet1"
				customerClusterName              = "arm64-vm-hcp-cluster"
				customerNodePoolName             = "arm64-vm-np-1"
			)
			// This pattern matches a subset of the smallest (2GiB) ARM64-capable VM sizes listed in https://issues.redhat.com/browse/ARO-22443
			vmSizePattern := regexp.MustCompile(`^Standard_D(?:2|4)pl(?:d)?s_v6$`)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "arm64-vm-cluster", tc.Location())
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
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
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

			By("discovering an ARM64-capable VM size")
			nodePoolVMSize, err := tc.FindVirtualMachineSizeMatching(ctx, vmSizePattern)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the ARM64-based node pool %q with VM size %q", customerNodePoolName, nodePoolVMSize))
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.VMSize = nodePoolVMSize

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
		})
})
