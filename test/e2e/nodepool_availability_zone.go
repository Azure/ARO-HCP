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
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const nonExistentAvailabilityZone = "99"

type availabilityZoneTestCluster struct {
	resourceGroupName        string
	managedResourceGroupName string
	clusterName              string
	nodePoolsClient          *hcpsdk20240610preview.NodePoolsClient
	tc                       *framework.E2ETestContext
}

func createAvailabilityZoneTestCluster(ctx context.Context) availabilityZoneTestCluster {
	const customerClusterName = "np-az-cluster"

	tc := framework.NewTestContext()

	if tc.UsePooledIdentities() {
		err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
		Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
	}

	By("creating a resource group")
	resourceGroup, err := tc.NewResourceGroup(ctx, "np-az", tc.Location())
	Expect(err).NotTo(HaveOccurred(), "failed to create resource group for nodepool availability zone tests")

	clusterParams := framework.NewDefaultClusterParams20240610()
	clusterParams.ClusterName = customerClusterName
	managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
	clusterParams.ManagedResourceGroupName = managedResourceGroupName

	By("creating customer resources")
	clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
		resourceGroup,
		clusterParams,
		map[string]any{},
		TestArtifactsFS,
		framework.RBACScopeResourceGroup,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for availability zone tests")

	By("creating the HCP cluster")
	err = tc.CreateHCPClusterFromParam20240610(ctx,
		GinkgoLogr,
		*resourceGroup.Name,
		clusterParams,
		framework.ClusterCreationTimeout,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

	return availabilityZoneTestCluster{
		resourceGroupName:        *resourceGroup.Name,
		managedResourceGroupName: managedResourceGroupName,
		clusterName:              customerClusterName,
		nodePoolsClient:          tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
		tc:                       tc,
	}
}

var _ = Describe("Nodepool Availability Zone", func() {
	It("should reject nodepool creation for invalid availability zone configurations",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			cluster := createAvailabilityZoneTestCluster(ctx)

			By("resolving a worker VM size that supports availability zones")
			selector := framework.DefaultWorkerVMSizeSelector()
			selector.RequireZones = true
			workerVMSize, err := cluster.tc.SelectVMSize(ctx, selector)
			if errors.Is(err, framework.ErrNoUsableVMSize) {
				Skip(fmt.Sprintf("no zone-capable worker VM size found in region %s", cluster.tc.Location()))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a worker VM size with availability zone support")
			availableZones, err := cluster.tc.AvailableZones(ctx, workerVMSize)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve usable availability zones for worker VM size %s", workerVMSize)
			Expect(availableZones).NotTo(BeEmpty(),
				"expected worker VM size %s selected with RequireZones to have usable availability zones in %s",
				workerVMSize, cluster.tc.Location())
			supportedAvailabilityZone := availableZones[0]

			By("attempting to create a nodepool with a non-existing availability zone")
			invalidZoneNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			invalidZoneNodePoolParams.ClusterName = cluster.clusterName
			invalidZoneNodePoolParams.NodePoolName = "np-bad-az"
			invalidZoneNodePoolParams.VMSize = workerVMSize
			invalidZoneNodePoolParams.AvailabilityZone = nonExistentAvailabilityZone
			err = cluster.tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				cluster.resourceGroupName,
				cluster.managedResourceGroupName,
				cluster.clusterName,
				invalidZoneNodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).To(HaveOccurred(), "expected nodepool creation to fail for non-existing availability zone %s", nonExistentAvailabilityZone)
			errMsg := strings.ToLower(err.Error())
			Expect(errMsg).To(ContainSubstring("does not support availability zone"),
				"error should indicate the availability zone is not supported")
			Expect(errMsg).To(ContainSubstring(nonExistentAvailabilityZone),
				"error should reference the invalid availability zone %s", nonExistentAvailabilityZone)

			failedNodePool, err := framework.GetNodePool20240610(ctx, cluster.nodePoolsClient, cluster.resourceGroupName, cluster.clusterName, invalidZoneNodePoolParams.NodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s after expected availability zone failure", invalidZoneNodePoolParams.NodePoolName)
			Expect(failedNodePool.Properties).NotTo(BeNil(), "nodepool response Properties was nil")
			Expect(failedNodePool.Properties.ProvisioningState).NotTo(BeNil(), "nodepool Properties.ProvisioningState was nil")
			Expect(*failedNodePool.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateFailed),
				"expected nodepool %s provisioning state to be Failed", invalidZoneNodePoolParams.NodePoolName)

			By("attempting to create a nodepool with an availability zone on a VM size that does not support zone deployment")
			vmSizeWithoutZones, err := cluster.tc.FindVMSizeWithoutAvailabilityZones(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to search for VM size without availability zone support")
			if vmSizeWithoutZones == "" {
				Skip(fmt.Sprintf("no Azure VM size without availability zone support found in region %s", cluster.tc.Location()))
			}

			noZoneVMNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			noZoneVMNodePoolParams.ClusterName = cluster.clusterName
			noZoneVMNodePoolParams.NodePoolName = "np-vm-no-az"
			noZoneVMNodePoolParams.VMSize = vmSizeWithoutZones
			noZoneVMNodePoolParams.AvailabilityZone = supportedAvailabilityZone
			err = cluster.tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				cluster.resourceGroupName,
				cluster.managedResourceGroupName,
				cluster.clusterName,
				noZoneVMNodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).To(HaveOccurred(),
				"expected nodepool creation to fail when VM size %s does not support availability zone deployment", vmSizeWithoutZones)
			Expect(strings.ToLower(err.Error())).To(ContainSubstring(
				"does not support to be deployed to a specific availability zone"),
				"error should indicate the VM size does not support availability zone deployment")
		})
})
