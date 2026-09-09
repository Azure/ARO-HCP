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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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
		labels.MIContainers(1),
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
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"assignKeyVaultCryptoOfficer":       true,
					"enableKeyVaultSoftDelete":          true,
					"enableKeyVaultPurgeProtection":     true,
					"keyVaultSoftDeleteRetentionInDays": 7,
				},
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

			By("resolving cluster-api-azure managed identity principal ID")
			Expect(clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators).NotTo(BeNil(), "cluster params missing ControlPlaneOperators")
			clusterAPIAzureResourceIDStr := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators["cluster-api-azure"]
			Expect(clusterAPIAzureResourceIDStr).NotTo(BeNil(), "cluster params missing cluster-api-azure identity")
			clusterAPIAzureResourceID, err := azcorearm.ParseResourceID(*clusterAPIAzureResourceIDStr)
			Expect(err).NotTo(HaveOccurred(), "failed to parse cluster-api-azure resource ID")
			clusterAPIAzureMI, err := msiClientFactory.NewUserAssignedIdentitiesClient().Get(ctx, clusterAPIAzureResourceID.ResourceGroupName, clusterAPIAzureResourceID.Name, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster-api-azure managed identity")
			Expect(clusterAPIAzureMI.Properties.PrincipalID).NotTo(BeNil(), "cluster-api-azure managed identity has no principal ID")

			By("creating disk encryption set backed by KeyVault")
			desResourceID, err := tc.CreateDiskEncryptionSet(ctx, *resourceGroup.Name, clusterParams.KeyVaultName, customerClusterName, tc.Location(), *serviceMI.Properties.PrincipalID, *clusterAPIAzureMI.Properties.PrincipalID)
			Expect(err).NotTo(HaveOccurred(), "failed to create disk encryption set")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("creating the nodepool with disk encryption set")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.EncryptionSetID = desResourceID

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s with DES", customerNodePoolName)

			By("verifying nodepool ARM resource has encryptionSetId")
			created, err := framework.GetNodePool20251223(ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s", customerNodePoolName)
			Expect(created.Properties).ToNot(BeNil(), "nodepool Properties was nil")
			Expect(created.Properties.ProvisioningState).ToNot(BeNil(), "nodepool ProvisioningState was nil")
			Expect(*created.Properties.ProvisioningState).To(Equal(hcpsdk20251223preview.ProvisioningStateSucceeded), "nodepool %s should be Succeeded", customerNodePoolName)
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

			By("verifying cluster health, node readiness, and VM OS disk encryption in parallel")
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyNodeCount(customerClusterName, int(nodePoolParams.Replicas)),
				verifiers.VerifyNodesReady(),
				verifiers.VerifyVMOSDiskCustomerEncryption(computeFactory, managedResourceGroupName, customerNodePoolName, desResourceID),
			)
			Expect(err).NotTo(HaveOccurred(), "cluster verification failed")
		})
})
