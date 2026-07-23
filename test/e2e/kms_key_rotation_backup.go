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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/Azure/ARO-HCP/internal/api"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Backups", func() {
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-07-31T00:00:00Z"))

	It("should trigger an on-demand backup after KMS key rotation completes",
		labels.RequireNothing, labels.High, labels.Positive, labels.CoreInfraService,
		labels.DevelopmentOnly, labels.AroRpApiCompatible, labels.Slow,
		func(ctx context.Context) {
			const clusterName = "kms-bkp-rotate"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "kms-bkp-rotate", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

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
				map[string]any{
					"assignKeyVaultCryptoOfficer": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By("creating the HCP cluster with version 4.22")
			err = tc.CreateHCPClusterFromParam20260630(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20260630preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260630preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)

			By("creating admin API HTTP client")
			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")

			By("creating a manual backup to capture current key version and cluster service ID")
			manualBackup, err := createBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to create manual on-demand backup")
			Expect(manualBackup.Name).NotTo(BeEmpty(), "manual backup name should not be empty")

			oldKeyVersion := manualBackup.KeyVersion
			Expect(oldKeyVersion).NotTo(BeEmpty(), "manual backup should have a key version for KMS-enabled cluster")

			// Manual backup names follow the pattern: {clusterServiceID}-{timestamp}.
			// clusterServiceID is a hex string (no dashes), so SplitN with limit 2 is safe.
			parts := strings.SplitN(manualBackup.Name, "-", 2)
			Expect(len(parts)).To(BeNumerically(">=", 2), "backup name should contain clusterServiceID-timestamp")
			clusterServiceID := parts[0]
			GinkgoLogr.Info("baseline backup captured", "clusterServiceID", clusterServiceID, "oldKeyVersion", oldKeyVersion)

			By("rotating the KMS key")
			keyVaultURL := fmt.Sprintf("https://%s.vault.azure.net/", clusterParams.KeyVaultName)
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			keyClient, err := azkeys.NewClient(keyVaultURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Key Vault client")

			createKeyResp, err := keyClient.CreateKey(ctx, clusterParams.EtcdEncryptionKeyName, azkeys.CreateKeyParameters{
				Kty:     to.Ptr(azkeys.KeyTypeRSA),
				KeySize: to.Ptr(int32(2048)),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create new key version")
			Expect(createKeyResp.Key).NotTo(BeNil(), "created key response was nil")
			Expect(createKeyResp.Key.KID).NotTo(BeNil(), "created key ID was nil")

			newKeyVersion := createKeyResp.Key.KID.Version()
			Expect(newKeyVersion).NotTo(BeEmpty(), "created key ID version was empty")

			GinkgoLogr.Info("rotated KMS key", "newKeyVersion", newKeyVersion)

			By("updating the cluster with the new KMS key version")
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
											Version: to.Ptr(newKeyVersion),
										},
									},
								},
							},
						},
					},
				},
				framework.HCPClusterReencryptionUpgradeTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to update cluster with new KMS key")
			Expect(updateResult.Properties).NotTo(BeNil(), "update result Properties was nil")

			GinkgoLogr.Info("cluster update completed, re-encryption finished",
				"provisioningState", *updateResult.Properties.ProvisioningState,
				"newKeyVersion", newKeyVersion)

			By("waiting for the key rotation backup to be created by the controller")
			expectedBackupName := fmt.Sprintf("%s-keyrotation-%s", clusterServiceID, newKeyVersion)
			GinkgoLogr.Info("polling for key rotation backup", "expectedBackupName", expectedBackupName)

			var lastPhase string
			Eventually(func() (string, error) {
				resp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, expectedBackupName)
				if err != nil {
					return "", err
				}
				if resp.Backup.Phase != lastPhase {
					GinkgoLogr.Info("key rotation backup phase", "backup", expectedBackupName, "phase", resp.Backup.Phase)
					lastPhase = resp.Backup.Phase
				}
				if resp.Backup.Phase == "PartiallyFailed" || resp.Backup.Phase == "Failed" {
					return "", fmt.Errorf("key rotation backup %s reached terminal failure state: %s", expectedBackupName, resp.Backup.Phase)
				}
				return resp.Backup.Phase, nil
			}, framework.BackupTimeout, framework.BackupWaitInterval).Should(Equal("Completed"),
				"key rotation backup should be created by the controller and reach Completed phase")

			By("verifying the key rotation backup has the correct key version")
			getResp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, expectedBackupName)
			Expect(err).NotTo(HaveOccurred(), "failed to get key rotation backup details")
			Expect(getResp.Backup.Name).To(Equal(expectedBackupName), "backup name should match expected key rotation backup name")
			Expect(getResp.Backup.KeyVersion).To(Equal(newKeyVersion), "backup key version should match the rotated key version")
			Expect(getResp.Backup.KeyVersion).NotTo(Equal(oldKeyVersion), "key rotation backup key version should differ from pre-rotation version")
			Expect(getResp.Backup.Phase).To(Equal("Completed"), "backup phase should be Completed")

			GinkgoLogr.Info("key rotation backup verified",
				"backupName", getResp.Backup.Name,
				"newKeyVersion", getResp.Backup.KeyVersion,
				"oldKeyVersion", oldKeyVersion,
				"phase", getResp.Backup.Phase)

			// Track all key versions to verify stale backup cleanup after the loop.
			allKeyVersions := []string{newKeyVersion}

			// TODO: repeating for a second key rotation to ensure the controller cleans up old desires and creates a new backup
			// this is for draft, the final PR will have a single rotation.
			for i := 0; i < 5; i++ {
				createKeyResp, err = keyClient.CreateKey(ctx, clusterParams.EtcdEncryptionKeyName, azkeys.CreateKeyParameters{
					Kty:     to.Ptr(azkeys.KeyTypeRSA),
					KeySize: to.Ptr(int32(2048)),
				}, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to create new key version")
				Expect(createKeyResp.Key).NotTo(BeNil(), "created key response was nil")
				Expect(createKeyResp.Key.KID).NotTo(BeNil(), "created key ID was nil")

				newKeyVersion = createKeyResp.Key.KID.Version()
				Expect(newKeyVersion).NotTo(BeEmpty(), "created key ID version was empty")
				allKeyVersions = append(allKeyVersions, newKeyVersion)

				GinkgoLogr.Info("rotated KMS key", "newKeyVersion", newKeyVersion)

				By("updating the cluster with the new KMS key version")
				hcpClient = tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
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
												Version: to.Ptr(newKeyVersion),
											},
										},
									},
								},
							},
						},
					},
					framework.HCPClusterReencryptionUpgradeTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to update cluster with new KMS key")
				Expect(updateResult.Properties).NotTo(BeNil(), "update result Properties was nil")

				GinkgoLogr.Info("cluster update completed, re-encryption finished",
					"provisioningState", *updateResult.Properties.ProvisioningState,
					"newKeyVersion", newKeyVersion)

				By("waiting for the key rotation backup to be created by the controller")
				expectedBackupName = fmt.Sprintf("%s-keyrotation-%s", clusterServiceID, newKeyVersion)
				GinkgoLogr.Info("polling for key rotation backup", "expectedBackupName", expectedBackupName)

				lastPhase = ""
				Eventually(func() (string, error) {
					resp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, expectedBackupName)
					if err != nil {
						return "", err
					}
					if resp.Backup.Phase != lastPhase {
						GinkgoLogr.Info("key rotation backup phase", "backup", expectedBackupName, "phase", resp.Backup.Phase)
						lastPhase = resp.Backup.Phase
					}
					if resp.Backup.Phase == "PartiallyFailed" || resp.Backup.Phase == "Failed" {
						return "", fmt.Errorf("key rotation backup %s reached terminal failure state: %s", expectedBackupName, resp.Backup.Phase)
					}
					return resp.Backup.Phase, nil
				}, framework.BackupTimeout, framework.BackupWaitInterval).Should(Equal("Completed"),
					"key rotation backup should be created by the controller and reach Completed phase")

				By("verifying the key rotation backup has the correct key version")
				getResp, err = getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, expectedBackupName)
				Expect(err).NotTo(HaveOccurred(), "failed to get key rotation backup details")
				Expect(getResp.Backup.Name).To(Equal(expectedBackupName), "backup name should match expected key rotation backup name")
				Expect(getResp.Backup.KeyVersion).To(Equal(newKeyVersion), "backup key version should match the rotated key version")
				Expect(getResp.Backup.KeyVersion).NotTo(Equal(oldKeyVersion), "key rotation backup key version should differ from pre-rotation version")
				Expect(getResp.Backup.Phase).To(Equal("Completed"), "backup phase should be Completed")

				GinkgoLogr.Info("key rotation backup verified",
					"backupName", getResp.Backup.Name,
					"newKeyVersion", getResp.Backup.KeyVersion,
					"oldKeyVersion", oldKeyVersion,
					"phase", getResp.Backup.Phase)
			}

			// TODO: write a proper test for the cleanup of stale key rotation backups.
			// For now, we just verify that the N-2 and older backups are deleted, while the N-1 backup remains,
			// and the manual backup is unaffected.

			By("verifying stale key rotation backups are deleted")
			staleVersions := allKeyVersions[:len(allKeyVersions)-2]
			for _, staleVersion := range staleVersions {
				staleBackupName := fmt.Sprintf("%s-keyrotation-%s", clusterServiceID, staleVersion)
				Eventually(func() error {
					_, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, staleBackupName)
					return err
				}, framework.BackupTimeout, framework.BackupWaitInterval).Should(HaveOccurred(),
					fmt.Sprintf("stale backup %s should be deleted after key rotation", staleBackupName))
				GinkgoLogr.Info("confirmed stale backup deleted", "backupName", staleBackupName)
			}

			By("verifying N-1 backup still exists")
			nMinus1Version := allKeyVersions[len(allKeyVersions)-2]
			nMinus1BackupName := fmt.Sprintf("%s-keyrotation-%s", clusterServiceID, nMinus1Version)
			nMinus1Resp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, nMinus1BackupName)
			Expect(err).NotTo(HaveOccurred(), "N-1 backup should still exist")
			Expect(nMinus1Resp.Backup.Name).To(Equal(nMinus1BackupName), "N-1 backup name should match")

			By("verifying manual backup is not affected by key rotation cleanup")
			manualResp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, manualBackup.Name)
			Expect(err).NotTo(HaveOccurred(), "manual backup should not be deleted by key rotation cleanup")
			Expect(manualResp.Backup.Name).To(Equal(manualBackup.Name), "manual backup name should match")
		})
})
