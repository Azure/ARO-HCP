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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Nodepool Ephemeral OS Disk", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should create a nodepool with ephemeral OS disk when autoRepair is enabled",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "ephemeral-disk"
				customerNodePoolName = "ephemeral-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ephemeral-osdisk", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for ephemeral OS disk test")
			useOldAPI, err := tc.APIVersionAvailable(ctx, *resourceGroup.Name, metadataapi.APIVersionV20251223Preview)
			Expect(err).NotTo(HaveOccurred(), "failed to check whether v20251223preview is available")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
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

			By("selecting a VM size that supports ephemeral OS disks")
			vmSize, err := tc.SelectVMSize(ctx, framework.EphemeralOSDiskWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to select a VM size with ephemeral OS disk support; "+
				"this typically indicates a SKU restriction or quota issue in the test subscription/region")

			By("creating nodepool with ephemeral OS disk and autoRepair enabled")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.VMSize = vmSize
			nodePoolParams.DiskType = hcpsdk20260901preview.OsDiskTypeEphemeral
			nodePoolParams.AutoRepair = true
			var getDiskProfile func() (string, bool, error)
			if useOldAPI {
				oldParams := framework.NewDefaultNodePoolParams20251223()
				oldParams.ClusterName = customerClusterName
				oldParams.NodePoolName = customerNodePoolName
				oldParams.VMSize = vmSize
				oldParams.DiskType = hcpsdk20251223preview.OsDiskTypeEphemeral
				oldParams.AutoRepair = true
				err = tc.CreateNodePoolFromParam20251223(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, customerClusterName, oldParams, framework.NodePoolCreationTimeout)
				getDiskProfile = func() (string, bool, error) {
					pool, err := framework.GetNodePool20251223(ctx, tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(), *resourceGroup.Name, customerClusterName, customerNodePoolName)
					if err != nil {
						return "", false, err
					}
					Expect(pool.Properties).NotTo(BeNil(), "v20251223preview nodepool Properties was nil")
					Expect(pool.Properties.Platform).NotTo(BeNil(), "v20251223preview nodepool Platform was nil")
					Expect(pool.Properties.Platform.OSDisk).NotTo(BeNil(), "v20251223preview nodepool OSDisk was nil")
					Expect(pool.Properties.Platform.OSDisk.DiskType).NotTo(BeNil(), "v20251223preview nodepool DiskType was nil")
					Expect(pool.Properties.AutoRepair).NotTo(BeNil(), "v20251223preview nodepool AutoRepair was nil")
					return string(*pool.Properties.Platform.OSDisk.DiskType), *pool.Properties.AutoRepair, nil
				}
			} else {
				err = tc.CreateNodePoolFromParam20260901(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, customerClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
				getDiskProfile = func() (string, bool, error) {
					pool, err := framework.GetNodePool20260901(ctx, tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient(), *resourceGroup.Name, customerClusterName, customerNodePoolName)
					if err != nil {
						return "", false, err
					}
					Expect(pool.Properties).NotTo(BeNil(), "v20260901preview nodepool Properties was nil")
					Expect(pool.Properties.Platform).NotTo(BeNil(), "v20260901preview nodepool Platform was nil")
					Expect(pool.Properties.Platform.OSDisk).NotTo(BeNil(), "v20260901preview nodepool OSDisk was nil")
					Expect(pool.Properties.Platform.OSDisk.DiskType).NotTo(BeNil(), "v20260901preview nodepool DiskType was nil")
					Expect(pool.Properties.AutoRepair).NotTo(BeNil(), "v20260901preview nodepool AutoRepair was nil")
					return string(*pool.Properties.Platform.OSDisk.DiskType), *pool.Properties.AutoRepair, nil
				}
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s with ephemeral OS disk", customerNodePoolName)

			By("verifying nodepool ARM resource has diskType=Ephemeral")
			for _, check := range []string{"initial GET", "round-trip GET"} {
				diskType, autoRepair, err := getDiskProfile()
				Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s for %s", customerNodePoolName, check)
				Expect(diskType).To(Equal("Ephemeral"), "nodepool %q DiskType should be Ephemeral on %s", customerNodePoolName, check)
				Expect(autoRepair).To(BeTrue(), "nodepool %q AutoRepair should be true on %s", customerNodePoolName, check)
			}

			By("getting credentials to verify cluster health")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %s is viable", customerClusterName)

			By("verifying count and ready status of nodes from the ephemeral nodepool")
			Expect(verifiers.VerifyNodeCount(customerClusterName, int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count matches expected replicas %d", nodePoolParams.Replicas)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready")

			By("verifying Azure VMs actually have ephemeral OS disks")
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			vms, err := framework.GetVirtualMachinesInResourceGroup(ctx, computeFactory, managedResourceGroupName, int(nodePoolParams.Replicas))
			Expect(err).NotTo(HaveOccurred(), "failed to get VMs in managed resource group %s", managedResourceGroupName)

			workerVMs := filterNodePoolVMs(vms, customerNodePoolName)
			By(fmt.Sprintf("found %d VMs for nodepool %s (out of %d total VMs in managed RG)", len(workerVMs), customerNodePoolName, len(vms)))
			Expect(workerVMs).ToNot(BeEmpty(), "expected at least one VM for nodepool %s", customerNodePoolName)

			for _, vm := range workerVMs {
				verifyVMHasEphemeralOSDisk(vm)
			}
		})

})

// filterNodePoolVMs filters VMs whose name contains the nodepool name.
// CAPZ derives VM names from the NodePool name via the MachineDeployment.
func filterNodePoolVMs(vms []*armcompute.VirtualMachine, nodePoolName string) []*armcompute.VirtualMachine {
	var matched []*armcompute.VirtualMachine
	for _, vm := range vms {
		if vm.Name != nil && strings.Contains(*vm.Name, nodePoolName) {
			matched = append(matched, vm)
		}
	}
	return matched
}

// verifyVMHasEphemeralOSDisk asserts that a VM has ephemeral OS disk configuration.
func verifyVMHasEphemeralOSDisk(vm *armcompute.VirtualMachine) {
	vmName := "<unknown>"
	if vm.Name != nil {
		vmName = *vm.Name
	}

	Expect(vm.Properties).ToNot(BeNil(), "VM %s has no properties", vmName)
	Expect(vm.Properties.StorageProfile).ToNot(BeNil(), "VM %s has no storage profile", vmName)
	Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil(), "VM %s has no OS disk", vmName)

	osDisk := vm.Properties.StorageProfile.OSDisk
	Expect(osDisk.DiffDiskSettings).ToNot(BeNil(),
		"VM %s has no DiffDiskSettings (expected for ephemeral disk)", vmName)
	Expect(osDisk.DiffDiskSettings.Option).ToNot(BeNil(),
		"VM %s DiffDiskSettings has no Option set", vmName)
	Expect(*osDisk.DiffDiskSettings.Option).To(Equal(armcompute.DiffDiskOptionsLocal),
		"VM %s has DiffDiskSettings.Option=%s, expected Local", vmName, *osDisk.DiffDiskSettings.Option)
}
