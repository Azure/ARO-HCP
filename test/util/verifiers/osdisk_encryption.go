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

package verifiers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

type verifyVMOSDiskCustomerEncryption struct {
	computeFactory        *armcompute.ClientFactory
	managedResourceGroup  string
	nodePoolName          string
	expectedDESResourceID string
}

func (v verifyVMOSDiskCustomerEncryption) Name() string {
	return fmt.Sprintf("VerifyVMOSDiskCustomerEncryption(nodePool=%s)", v.nodePoolName)
}

func (v verifyVMOSDiskCustomerEncryption) Verify(ctx context.Context, _ *rest.Config) error {
	vmClient := v.computeFactory.NewVirtualMachinesClient()
	disksClient := v.computeFactory.NewDisksClient()

	var vms []*armcompute.VirtualMachine
	pager := vmClient.NewListPager(v.managedResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list VMs in managed resource group %q: %w", v.managedResourceGroup, err)
		}
		vms = append(vms, page.Value...)
	}

	var workerVMs []*armcompute.VirtualMachine
	for _, vm := range vms {
		if vm.Name != nil && strings.Contains(*vm.Name, v.nodePoolName) {
			workerVMs = append(workerVMs, vm)
		}
	}
	if len(workerVMs) == 0 {
		return fmt.Errorf("no VMs found for nodepool %s in managed resource group %s", v.nodePoolName, v.managedResourceGroup)
	}

	var errs []error
	for _, vm := range workerVMs {
		if err := v.verifyVM(ctx, disksClient, vm); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("OS disk encryption verification failed for %d/%d VMs: %w", len(errs), len(workerVMs), errors.Join(errs...))
	}
	return nil
}

func (v verifyVMOSDiskCustomerEncryption) verifyVM(ctx context.Context, disksClient *armcompute.DisksClient, vm *armcompute.VirtualMachine) error {
	if vm.Name == nil {
		return fmt.Errorf("VM has no name")
	}
	vmName := *vm.Name

	if vm.Properties == nil || vm.Properties.StorageProfile == nil || vm.Properties.StorageProfile.OSDisk == nil || vm.Properties.StorageProfile.OSDisk.ManagedDisk == nil {
		return fmt.Errorf("VM %s missing storage profile or managed disk", vmName)
	}

	osDiskName := vm.Properties.StorageProfile.OSDisk.Name
	if osDiskName == nil {
		return fmt.Errorf("VM %s OS disk has no name", vmName)
	}

	disk, err := disksClient.Get(ctx, v.managedResourceGroup, *osDiskName, nil)
	if err != nil {
		return fmt.Errorf("failed to get disk %s for VM %s: %w", *osDiskName, vmName, err)
	}

	if disk.Properties == nil || disk.Properties.Encryption == nil {
		return fmt.Errorf("disk %s for VM %s has no encryption properties", *osDiskName, vmName)
	}
	if disk.Properties.Encryption.Type == nil {
		return fmt.Errorf("disk %s for VM %s has no encryption type", *osDiskName, vmName)
	}
	if *disk.Properties.Encryption.Type != armcompute.EncryptionTypeEncryptionAtRestWithCustomerKey {
		return fmt.Errorf("disk %s for VM %s has encryption type %s, expected EncryptionAtRestWithCustomerKey", *osDiskName, vmName, *disk.Properties.Encryption.Type)
	}
	if disk.Properties.Encryption.DiskEncryptionSetID == nil {
		return fmt.Errorf("disk %s for VM %s has no DiskEncryptionSetID", *osDiskName, vmName)
	}
	if !strings.EqualFold(*disk.Properties.Encryption.DiskEncryptionSetID, v.expectedDESResourceID) {
		return fmt.Errorf("disk %s for VM %s DiskEncryptionSetID mismatch: got %s, expected %s", *osDiskName, vmName, *disk.Properties.Encryption.DiskEncryptionSetID, v.expectedDESResourceID)
	}
	return nil
}

func VerifyVMOSDiskCustomerEncryption(computeFactory *armcompute.ClientFactory, managedResourceGroup, nodePoolName, expectedDESResourceID string) HostedClusterVerifier {
	return verifyVMOSDiskCustomerEncryption{
		computeFactory:        computeFactory,
		managedResourceGroup:  managedResourceGroup,
		nodePoolName:          nodePoolName,
		expectedDESResourceID: expectedDESResourceID,
	}
}
