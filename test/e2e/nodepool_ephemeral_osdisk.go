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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// mustParseDate parses a date string in "2006-01-02" format or panics.
func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(fmt.Sprintf("invalid date %q: %v", s, err))
	}
	return t
}

// isAPINotDeployedError returns true if the error indicates the API version
// has not been rolled out to this region yet.
func isAPINotDeployedError(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusNotFound ||
		strings.Contains(respErr.ErrorCode, "NoRegisteredProviderFound")
}

var _ = Describe("Nodepool Ephemeral OS Disk", func() {
	// Set deadline to a reasonable date after which we expect the v20251223preview
	// API to be deployed. Adjust as needed based on rollout schedule.
	timeBombDeadline := mustParseDate("2026-04-01")

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

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
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

			By("selecting a VM size that supports ephemeral OS disks")
			vmSize, err := tc.SelectVMSize(ctx, framework.EphemeralOSDiskWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to select a VM size with ephemeral OS disk support; "+
				"this typically indicates a SKU restriction or quota issue in the test subscription/region")

			By("creating nodepool with ephemeral OS disk and autoRepair enabled")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.VMSize = vmSize
			nodePoolParams.DiskType = hcpsdk20251223preview.OsDiskTypeEphemeral
			nodePoolParams.AutoRepair = true
			err = tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout)

			client20251223 := tc.Get20251223ClientFactoryOrDie(ctx)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20251223preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20251223preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s with ephemeral OS disk", customerNodePoolName)

			By("verifying nodepool ARM resource has diskType=Ephemeral from LRO result")
			created, err := framework.GetNodePool20251223(ctx, client20251223.NewNodePoolsClient(), *resourceGroup.Name, customerClusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s", customerNodePoolName)
			Expect(created.Properties).ToNot(BeNil(), "created nodepool response Properties was nil")
			Expect(created.Properties.Platform).ToNot(BeNil(), "created nodepool response Properties.Platform was nil")
			Expect(created.Properties.Platform.OSDisk).ToNot(BeNil(), "created nodepool response Properties.Platform.OSDisk was nil")
			Expect(created.Properties.Platform.OSDisk.DiskType).ToNot(BeNil(), "created nodepool response Properties.Platform.OSDisk.DiskType was nil")
			Expect(*created.Properties.Platform.OSDisk.DiskType).To(Equal(hcpsdk20251223preview.OsDiskTypeEphemeral), "expected created nodepool OSDisk.DiskType to be Ephemeral")
			Expect(created.Properties.AutoRepair).ToNot(BeNil(), "created nodepool response Properties.AutoRepair was nil")
			Expect(*created.Properties.AutoRepair).To(BeTrue(), "expected created nodepool AutoRepair to be true")
			By("confirming diskType and autoRepair persist via separate GET (round-trip verification)")
			fetched, err := framework.GetNodePool20251223(ctx,
				client20251223.NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to GET nodepool %s for round-trip verification", customerNodePoolName)
			Expect(fetched.Properties).ToNot(BeNil(), "fetched nodepool response Properties was nil")
			Expect(fetched.Properties.Platform).ToNot(BeNil(), "fetched nodepool response Properties.Platform was nil")
			Expect(fetched.Properties.Platform.OSDisk).ToNot(BeNil(), "fetched nodepool response Properties.Platform.OSDisk was nil")
			Expect(fetched.Properties.Platform.OSDisk.DiskType).ToNot(BeNil(), "fetched nodepool response Properties.Platform.OSDisk.DiskType was nil")
			Expect(*fetched.Properties.Platform.OSDisk.DiskType).To(Equal(hcpsdk20251223preview.OsDiskTypeEphemeral), "expected fetched nodepool OSDisk.DiskType to be Ephemeral")
			Expect(fetched.Properties.AutoRepair).ToNot(BeNil(), "fetched nodepool response Properties.AutoRepair was nil")
			Expect(*fetched.Properties.AutoRepair).To(BeTrue(), "expected fetched nodepool AutoRepair to be true")

			By("getting credentials to verify cluster health")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
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
