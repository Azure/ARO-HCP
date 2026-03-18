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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// timeBombSkip skips the test if the v20251223preview API has not yet been
// deployed to all regions. Once the deadline passes, the test fails instead
// of skipping, signaling that the API deployment is overdue and needs attention.
func timeBombSkip(deadline time.Time) {
	if time.Now().Before(deadline) {
		Skip(fmt.Sprintf("v20251223preview API not yet deployed to all regions; skipping until %s", deadline.Format(time.RFC3339)))
	}
	// After the deadline, do not skip — let the test run (and fail if the API still isn't deployed).
}

var _ = Describe("Nodepool Ephemeral OS Disk", func() {
	// Set deadline to a reasonable date after which we expect the v20251223preview
	// API to be deployed. Adjust as needed based on rollout schedule.
	timeBombDeadline := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should create a nodepool with ephemeral OS disk when autoRepair is enabled",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.Slow,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			timeBombSkip(timeBombDeadline)

			const (
				customerClusterName  = "ephemeral-disk"
				customerNodePoolName = "ephemeral-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ephemeral-osdisk", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating nodepool with ephemeral OS disk and autoRepair enabled")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePool := buildNodePoolWithDiskType(
				nodePoolParams,
				tc.Location(),
				hcpsdk20251223preview.OsDiskTypeEphemeral,
				true,
			)

			client20251223 := tc.Get20251223ClientFactoryOrDie(ctx)
			created, err := framework.CreateNodePoolAndWait20251223(
				ctx,
				client20251223.NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				nodePool,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodepool ARM resource has diskType=Ephemeral from LRO result")
			Expect(created.Properties).ToNot(BeNil())
			Expect(created.Properties.Platform).ToNot(BeNil())
			Expect(created.Properties.Platform.OSDisk).ToNot(BeNil())
			Expect(created.Properties.Platform.OSDisk.DiskType).ToNot(BeNil())
			Expect(*created.Properties.Platform.OSDisk.DiskType).To(Equal(hcpsdk20251223preview.OsDiskTypeEphemeral))
			Expect(created.Properties.AutoRepair).ToNot(BeNil())
			Expect(*created.Properties.AutoRepair).To(BeTrue())

			By("confirming diskType and autoRepair persist via separate GET (round-trip verification)")
			fetched, err := framework.GetNodePool20251223(ctx,
				client20251223.NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetched.Properties).ToNot(BeNil())
			Expect(fetched.Properties.Platform).ToNot(BeNil())
			Expect(fetched.Properties.Platform.OSDisk).ToNot(BeNil())
			Expect(fetched.Properties.Platform.OSDisk.DiskType).ToNot(BeNil())
			Expect(*fetched.Properties.Platform.OSDisk.DiskType).To(Equal(hcpsdk20251223preview.OsDiskTypeEphemeral))
			Expect(fetched.Properties.AutoRepair).ToNot(BeNil())
			Expect(*fetched.Properties.AutoRepair).To(BeTrue())

			By("getting credentials to verify cluster health")
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

			By("verifying Azure VMs actually have ephemeral OS disks")
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			vms, err := framework.GetVirtualMachinesInResourceGroup(ctx, computeFactory, managedResourceGroupName, int(nodePoolParams.Replicas), 5*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			workerVMs := filterNodePoolVMs(vms, customerNodePoolName)
			By(fmt.Sprintf("found %d VMs for nodepool %s (out of %d total VMs in managed RG)", len(workerVMs), customerNodePoolName, len(vms)))
			Expect(workerVMs).ToNot(BeEmpty(), "expected at least one VM for nodepool %s", customerNodePoolName)

			for _, vm := range workerVMs {
				verifyVMHasEphemeralOSDisk(vm)
			}
		})

	It("should reject ephemeral OS disk creation when autoRepair is false",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			timeBombSkip(timeBombDeadline)

			// Validation is frontend-synchronous (returns 400 immediately), but
			// a parent cluster must exist for the frontend to route the request.
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ephemeral-neg", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters for a minimal cluster")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = "ephemeral-neg"
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster as nodepool parent")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create nodepool with ephemeral disk but autoRepair=false")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePool := buildNodePoolWithDiskType(
				nodePoolParams,
				tc.Location(),
				hcpsdk20251223preview.OsDiskTypeEphemeral,
				false, // autoRepair=false is invalid with ephemeral disks
			)

			By("verifying the creation is rejected with a 400 Bad Request")
			// Validation is frontend-synchronous: the RP returns 400 from the initial
			// PUT without starting an async operation.
			client20251223 := tc.Get20251223ClientFactoryOrDie(ctx)
			_, err = client20251223.NewNodePoolsClient().BeginCreateOrUpdate(
				ctx,
				*resourceGroup.Name,
				"ephemeral-neg",
				"bad-ephemeral",
				nodePool,
				nil,
			)
			Expect(err).To(HaveOccurred(), "expected ephemeral disk with autoRepair=false to be rejected")

			var respErr *azcore.ResponseError
			Expect(errors.As(err, &respErr)).To(BeTrue(), "expected azcore.ResponseError, got %T", err)
			Expect(respErr.StatusCode).To(Equal(http.StatusBadRequest), "expected 400 Bad Request, got %d", respErr.StatusCode)
			Expect(respErr.ErrorCode).ToNot(BeEmpty(), "ARM error response must include a non-empty error code")
		})
})

// buildNodePoolWithDiskType builds a v20251223preview NodePool with specific diskType and autoRepair settings.
func buildNodePoolWithDiskType(
	params framework.NodePoolParams,
	location string,
	diskType hcpsdk20251223preview.OsDiskType,
	autoRepair bool,
) hcpsdk20251223preview.NodePool {
	return hcpsdk20251223preview.NodePool{
		Location: to.Ptr(location),
		Properties: &hcpsdk20251223preview.NodePoolProperties{
			Version: &hcpsdk20251223preview.NodePoolVersionProfile{
				ID:           to.Ptr(params.OpenshiftVersionId),
				ChannelGroup: to.Ptr(params.ChannelGroup),
			},
			Replicas: to.Ptr(params.Replicas),
			Platform: &hcpsdk20251223preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(params.VMSize),
				OSDisk: &hcpsdk20251223preview.OsDiskProfile{
					SizeGiB:                to.Ptr(params.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20251223preview.DiskStorageAccountType(params.DiskStorageAccountType)),
					DiskType:               to.Ptr(diskType),
				},
			},
			AutoRepair: to.Ptr(autoRepair),
		},
	}
}

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
