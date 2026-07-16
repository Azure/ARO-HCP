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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Nodepool OS Disk Encryption", func() {
	It("should create a nodepool with customer-managed disk encryption via DES",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "des-encrypt"
				customerNodePoolName = "des-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "des-encrypt", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

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

			By("resolving service managed identity principal ID")
			Expect(clusterParams.UserAssignedIdentitiesProfile).NotTo(BeNil(), "cluster params missing UserAssignedIdentitiesProfile")
			Expect(clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity).NotTo(BeNil(), "cluster params missing ServiceManagedIdentity resource ID")

			serviceMIResourceID, err := azcorearm.ParseResourceID(*clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity)
			Expect(err).NotTo(HaveOccurred(), "failed to parse service managed identity resource ID")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			creds, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credentials")

			msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client factory")

			serviceMI, err := msiClientFactory.NewUserAssignedIdentitiesClient().Get(ctx, serviceMIResourceID.ResourceGroupName, serviceMIResourceID.Name, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get service managed identity")
			Expect(serviceMI.Properties.PrincipalID).NotTo(BeNil(), "service managed identity has no principal ID")

			By("creating disk encryption set backed by KeyVault")
			desDeployment, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/disk-encryption-set.json"),
				framework.WithDeploymentName(fmt.Sprintf("des-%s", customerClusterName)),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"keyVaultName":         clusterParams.KeyVaultName,
					"clusterName":          customerClusterName,
					"serviceMiPrincipalId": *serviceMI.Properties.PrincipalID,
				}),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create disk encryption set")

			desResourceID, err := framework.GetOutputValueString(desDeployment, "diskEncryptionSetId")
			Expect(err).NotTo(HaveOccurred(), "failed to get diskEncryptionSetId from deployment output")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("creating the nodepool with disk encryption set")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.EncryptionSetID = desResourceID

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s with DES", customerNodePoolName)

			By("verifying nodepool ARM resource has encryptionSetId")
			created, err := framework.GetNodePool20240610(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s", customerNodePoolName)
			Expect(created.Properties).ToNot(BeNil(), "nodepool Properties was nil")
			Expect(created.Properties.ProvisioningState).ToNot(BeNil(), "nodepool ProvisioningState was nil")
			Expect(*created.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "nodepool %s should be Succeeded", customerNodePoolName)
			Expect(created.Properties.Platform).ToNot(BeNil(), "nodepool Platform was nil")
			Expect(created.Properties.Platform.OSDisk).ToNot(BeNil(), "nodepool OSDisk was nil")
			Expect(created.Properties.Platform.OSDisk.EncryptionSetID).ToNot(BeNil(),
				"nodepool OSDisk.EncryptionSetID should be set")
			Expect(strings.EqualFold(*created.Properties.Platform.OSDisk.EncryptionSetID, desResourceID)).To(BeTrue(),
				"nodepool OSDisk.EncryptionSetID should match the DES resource ID")

			By("getting credentials to verify cluster health")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health")

			By("verifying Azure VM OS disks use customer-managed encryption")
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			vms, err := framework.GetVirtualMachinesInResourceGroup(ctx, computeFactory, managedResourceGroupName, int(nodePoolParams.Replicas))
			Expect(err).NotTo(HaveOccurred(), "failed to list VMs in managed resource group %s", managedResourceGroupName)

			workerVMs := filterNodePoolVMs(vms, customerNodePoolName)
			By(fmt.Sprintf("found %d VMs for nodepool %s", len(workerVMs), customerNodePoolName))
			Expect(workerVMs).ToNot(BeEmpty(), "expected at least one VM for nodepool %s", customerNodePoolName)

			By("verifying each VM's OS disk has EncryptionAtRestWithCustomerKey")
			disksClient := computeFactory.NewDisksClient()
			for _, vm := range workerVMs {
				verifyVMOSDiskCustomerEncryption(ctx, disksClient, managedResourceGroupName, vm, desResourceID)
			}
		})
})

func verifyVMOSDiskCustomerEncryption(ctx context.Context, disksClient *armcompute.DisksClient, managedResourceGroup string, vm *armcompute.VirtualMachine, expectedDESResourceID string) {
	Expect(vm.Name).ToNot(BeNil(), "VM has no name")
	vmName := *vm.Name

	Expect(vm.Properties).ToNot(BeNil(), "VM %s has no properties", vmName)
	Expect(vm.Properties.StorageProfile).ToNot(BeNil(), "VM %s has no storage profile", vmName)
	Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil(), "VM %s has no OS disk", vmName)
	Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil(), "VM %s has no managed disk", vmName)

	osDiskName := vm.Properties.StorageProfile.OSDisk.Name
	Expect(osDiskName).ToNot(BeNil(), "VM %s OS disk has no name", vmName)

	disk, err := disksClient.Get(ctx, managedResourceGroup, *osDiskName, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to get disk %s for VM %s", *osDiskName, vmName)

	Expect(disk.Properties).ToNot(BeNil(), "disk %s has no properties", *osDiskName)
	Expect(disk.Properties.Encryption).ToNot(BeNil(), "disk %s has no encryption properties", *osDiskName)
	Expect(disk.Properties.Encryption.Type).ToNot(BeNil(), "disk %s has no encryption type", *osDiskName)
	Expect(*disk.Properties.Encryption.Type).To(Equal(armcompute.EncryptionTypeEncryptionAtRestWithCustomerKey),
		"disk %s for VM %s should have EncryptionAtRestWithCustomerKey, got %s",
		*osDiskName, vmName, *disk.Properties.Encryption.Type)
	Expect(disk.Properties.Encryption.DiskEncryptionSetID).ToNot(BeNil(),
		"disk %s for VM %s should have a DiskEncryptionSetID", *osDiskName, vmName)
	Expect(strings.EqualFold(*disk.Properties.Encryption.DiskEncryptionSetID, expectedDESResourceID)).To(BeTrue(),
		"disk %s for VM %s DiskEncryptionSetID mismatch: got %s, expected %s",
		*osDiskName, vmName, *disk.Properties.Encryption.DiskEncryptionSetID, expectedDESResourceID)
}
