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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const HCPClusterReencryptionUpgradeTimeout = 18 * time.Minute

var _ = Describe("Customer", func() {
	// Deadline for v20260630preview API deployment in non-dev environments
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-08-07T00:00:00Z"))

	It("should be able to rotate KMS key for a cluster with version >= 4.22",
		labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow,
		func(ctx context.Context) {
			const clusterName = "kms-key-rotate-422"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "kms-key-rotate", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for KMS key rotation test")

			By("creating cluster parameters with version 4.22")
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = "4.22"

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"assignKeyVaultCryptoOfficer": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for KMS key rotation cluster")

			By("creating the HCP cluster with version 4.22")
			err = tc.CreateHCPClusterFromParam20260630(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil, // imageDigestMirrors
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20260630preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260630preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for KMS key rotation test")

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability for update")

			By("rotating the KMS key")
			keyVaultURL := fmt.Sprintf("https://%s.vault.azure.net/", clusterParams.KeyVaultName)
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			keyClient, err := azkeys.NewClient(keyVaultURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Key Vault client")

			originalKeyVersion := clusterParams.EtcdEncryptionKeyVersion

			GinkgoLogr.Info("Creating new key version (rotation)",
				"keyVaultName", clusterParams.KeyVaultName,
				"keyName", clusterParams.EtcdEncryptionKeyName,
				"originalVersion", originalKeyVersion)

			createKeyResp, err := keyClient.CreateKey(ctx, clusterParams.EtcdEncryptionKeyName, azkeys.CreateKeyParameters{
				Kty:     to.Ptr(azkeys.KeyTypeRSA),
				KeySize: to.Ptr(int32(2048)),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create new key version (rotation)")
			Expect(createKeyResp.Key).NotTo(BeNil(), "created key response was nil")
			Expect(createKeyResp.Key.KID).NotTo(BeNil(), "created key ID was nil")

			firstKeyVersion := createKeyResp.Key.KID.Version()
			Expect(firstKeyVersion).NotTo(BeEmpty(), "created key ID version was empty")

			GinkgoLogr.Info("Successfully created new key version",
				"keyVaultName", clusterParams.KeyVaultName,
				"keyName", clusterParams.EtcdEncryptionKeyName,
				"newVersion", firstKeyVersion)

			By("updating the cluster with the new KMS key")
			hcpClient := tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			updateResult, err := framework.UpdateHCPCluster20260630(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				hcpsdk20260630preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20260630preview.HcpOpenShiftClusterPropertiesUpdate{
						Etcd: &hcpsdk20260630preview.EtcdProfileUpdate{
							DataEncryption: &hcpsdk20260630preview.EtcdDataEncryptionProfileUpdate{
								CustomerManaged: &hcpsdk20260630preview.CustomerManagedEncryptionProfileUpdate{
									Kms: &hcpsdk20260630preview.KmsEncryptionProfileUpdate{
										ActiveKey: &hcpsdk20260630preview.KmsKeyUpdate{
											Version: to.Ptr(firstKeyVersion),
										},
									},
								},
							},
						},
					},
				},
				HCPClusterReencryptionUpgradeTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to update cluster with new KMS key")

			Expect(updateResult.Properties).NotTo(BeNil(), "update result Properties was nil")
			Expect(updateResult.Properties.ProvisioningState).NotTo(BeNil(), "update result ProvisioningState was nil")

			GinkgoLogr.Info("Cluster update completed successfully",
				"clusterName", clusterName,
				"provisioningState", *updateResult.Properties.ProvisioningState,
				"newKeyVersion", firstKeyVersion)

			By("verifying the cluster references the new KMS key version")
			Expect(updateResult.Properties.Etcd).NotTo(BeNil(), "update result Etcd was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption).NotTo(BeNil(), "update result DataEncryption was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "update result CustomerManaged was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "update result Kms was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "update result ActiveKey was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "update result key Version was nil")
			Expect(*updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(firstKeyVersion),
				"cluster should reference the new KMS key version after update")

			By("confirming key version persists via GET (round-trip verification)")
			fetchedCluster, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster for round-trip verification")
			Expect(fetchedCluster.Properties).NotTo(BeNil(), "fetched cluster Properties was nil")
			Expect(fetchedCluster.Properties.Etcd).NotTo(BeNil(), "fetched cluster Etcd was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "fetched cluster DataEncryption was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "fetched cluster CustomerManaged was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "fetched cluster Kms was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "fetched cluster ActiveKey was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "fetched cluster key Version was nil")
			Expect(*fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(firstKeyVersion),
				"cluster should reference the new KMS key version after round-trip GET")

			By("verifying StorageVersionMigration succeeded for re-encryption")
			err = verifiers.VerifyStorageVersionMigrationSucceeded().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "all StorageVersionMigration resources should reach Succeeded state after KMS key rotation")

			By("disabling first key version")
			keyParams := azkeys.UpdateKeyParameters{
				KeyAttributes: &azkeys.KeyAttributes{
					Enabled: to.Ptr(false),
				},
			}
			updateKeyResp, err := keyClient.UpdateKey(ctx, clusterParams.EtcdEncryptionKeyName, originalKeyVersion, keyParams, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to disable old KMS key version: %v", originalKeyVersion)
			Expect(updateKeyResp.Attributes.Enabled).ToNot(BeNil(), "update key response did not include enabled attribute")
			Expect(*updateKeyResp.Attributes.Enabled).To(BeFalse(), "old key version should be disabled")

			GinkgoLogr.Info("Old key version was disabled successfully",
				"keyVersion", originalKeyVersion,
				"enabled", updateKeyResp.Attributes.Enabled,
			)

			By("verify the Cluster is still fully functional")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyStorageVersionMigrationSucceeded())
			Expect(err).NotTo(HaveOccurred(), "all StorageVersionMigration resources should reach Succeeded state after KMS key rotation")

			By("rotating the KMS key a second time")

			GinkgoLogr.Info("Creating new key version (second rotation)",
				"keyVaultName", clusterParams.KeyVaultName,
				"keyName", clusterParams.EtcdEncryptionKeyName,
				"currentKeyVersion", firstKeyVersion)

			createKeyResp, err = keyClient.CreateKey(ctx, clusterParams.EtcdEncryptionKeyName, azkeys.CreateKeyParameters{
				Kty:     to.Ptr(azkeys.KeyTypeRSA),
				KeySize: to.Ptr(int32(2048)),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create new key version (second rotation)")
			Expect(createKeyResp.Key).NotTo(BeNil(), "created key response was nil")
			Expect(createKeyResp.Key.KID).NotTo(BeNil(), "created key ID was nil")

			secondKeyVersion := createKeyResp.Key.KID.Version()
			Expect(secondKeyVersion).NotTo(BeEmpty(), "created key ID version was empty")

			GinkgoLogr.Info("Successfully created new key version (second rotation)",
				"keyVaultName", clusterParams.KeyVaultName,
				"keyName", clusterParams.EtcdEncryptionKeyName,
				"newKeyVersion", secondKeyVersion)

			By("updating the cluster with the new KMS key (second rotation)")
			updateResult, err = framework.UpdateHCPCluster20260630(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				hcpsdk20260630preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20260630preview.HcpOpenShiftClusterPropertiesUpdate{
						Etcd: &hcpsdk20260630preview.EtcdProfileUpdate{
							DataEncryption: &hcpsdk20260630preview.EtcdDataEncryptionProfileUpdate{
								CustomerManaged: &hcpsdk20260630preview.CustomerManagedEncryptionProfileUpdate{
									Kms: &hcpsdk20260630preview.KmsEncryptionProfileUpdate{
										ActiveKey: &hcpsdk20260630preview.KmsKeyUpdate{
											Version: to.Ptr(secondKeyVersion),
										},
									},
								},
							},
						},
					},
				},
				HCPClusterReencryptionUpgradeTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to update cluster with new KMS key with the second rotation")

			Expect(updateResult.Properties).NotTo(BeNil(), "update result Properties was nil")
			Expect(updateResult.Properties.ProvisioningState).NotTo(BeNil(), "update result ProvisioningState was nil")

			GinkgoLogr.Info("Cluster update completed successfully (second rotation)",
				"clusterName", clusterName,
				"provisioningState", *updateResult.Properties.ProvisioningState,
				"newKeyVersion", secondKeyVersion)

			By("verifying the cluster references the new KMS key version (second rotation)")
			Expect(updateResult.Properties.Etcd).NotTo(BeNil(), "update result Etcd was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption).NotTo(BeNil(), "update result DataEncryption was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "update result CustomerManaged was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "update result Kms was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "update result ActiveKey was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "update result key Version was nil")
			Expect(*updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(secondKeyVersion),
				"cluster should reference the new KMS key version after update")

			By("confirming key version persists via GET for second rotation (round-trip verification)")
			fetchedCluster, err = hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster for round-trip verification")
			Expect(fetchedCluster.Properties).NotTo(BeNil(), "fetched cluster Properties was nil")
			Expect(fetchedCluster.Properties.Etcd).NotTo(BeNil(), "fetched cluster Etcd was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "fetched cluster DataEncryption was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "fetched cluster CustomerManaged was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "fetched cluster Kms was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "fetched cluster ActiveKey was nil")
			Expect(fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "fetched cluster key Version was nil")
			Expect(*fetchedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(secondKeyVersion),
				"cluster should reference the new KMS key version after round-trip GET")

			By("verifying StorageVersionMigration succeeded for re-encryption (second rotation)")
			err = verifiers.VerifyStorageVersionMigrationSucceeded().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "all StorageVersionMigration resources should reach Succeeded state after KMS key rotation")

			By("disabling the old key version (second rotation)")
			keyParams = azkeys.UpdateKeyParameters{
				KeyAttributes: &azkeys.KeyAttributes{
					Enabled: to.Ptr(false),
				},
			}
			updateKeyResp, err = keyClient.UpdateKey(ctx, clusterParams.EtcdEncryptionKeyName, firstKeyVersion, keyParams, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to disable old KMS key version: %v", firstKeyVersion)
			Expect(updateKeyResp.Attributes.Enabled).ToNot(BeNil(), "update key response did not include enabled attribute")
			Expect(*updateKeyResp.Attributes.Enabled).To(BeFalse(), "old key version should be disabled")

			GinkgoLogr.Info("First key version was disabled successfully",
				"keyVersion", firstKeyVersion,
				"enabled", updateKeyResp.Attributes.Enabled,
			)

			By("verify the Cluster is still fully functional after second rotation")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyStorageVersionMigrationSucceeded())
			Expect(err).NotTo(HaveOccurred(), "all StorageVersionMigration resources should reach Succeeded state after KMS key rotation")
		},
	)
})
