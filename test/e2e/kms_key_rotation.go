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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/Azure/ARO-HCP/admin/server/handlers/hcp"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// HCPClusterReencryptionUpgradeTimeout is the cluster update timeout plus additional time for etcd re-encryption.
// p99 across dev/stg/prod is 18m 29s (2026-09-01, 30d window).
// The total timeout should be 23m = p99 + ~20% buffer.
const HCPClusterReencryptionUpgradeTimeout = framework.UpdateHCPClusterTimeout + 13*time.Minute

var _ = Describe("Customer", func() {
	It("should be able to rotate KMS key for a cluster with version >= 4.22",
		labels.RequireNothing, labels.High, labels.Positive, labels.AroRpApiCompatible, labels.Slow,
		labels.MIContainers(1),
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

			By("creating cluster parameters with version 4.22 or higher")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			openshiftVersionID, err := framework.PickAtLeastOpenshiftVersionId(clusterParams.OpenshiftVersionId, "4.22")
			if framework.IsIncompatibleNightlyVersionError(err) {
				skipMsg := fmt.Sprintf("this test needs OCP >= 4.22, but default version %q does not satisfy it: %v", clusterParams.OpenshiftVersionId, err)
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to select OpenShift version >= 4.22 (default version: %q)", clusterParams.OpenshiftVersionId)
			clusterParams.OpenshiftVersionId = openshiftVersionID

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"assignKeyVaultCryptoOfficer": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for KMS key rotation cluster")

			By("creating the HCP cluster with version 4.22 or higher")
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil, // imageDigestMirrors
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for KMS key rotation test")

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability for update")

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				metadataapi.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)

			// The admin API is only reachable this way in dev environments; on-demand
			// backup verification below is skipped in higher environments.
			devEnv := framework.IsDevelopmentEnvironment()
			var httpClient *http.Client
			var adminAPIAddress string
			if devEnv {
				By("creating admin API HTTP client")
				httpClient, adminAPIAddress, err = tc.NewAdminAPIHTTPClient(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")

				By("waiting for backup schedules to be created")
				Eventually(func() (bool, error) {
					resp, err := getBackupScheduleViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
					if err != nil {
						return false, err
					}
					return len(resp.Schedules) > 0, nil
				}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
					"backup schedules should be created for the cluster")
			}

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
			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			updateResult, err := framework.UpdateHCPCluster20260901(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
						Etcd: &hcpsdk20260901preview.EtcdProfileUpdate{
							DataEncryption: &hcpsdk20260901preview.EtcdDataEncryptionProfileUpdate{
								CustomerManaged: &hcpsdk20260901preview.CustomerManagedEncryptionProfileUpdate{
									Kms: &hcpsdk20260901preview.KmsEncryptionProfileUpdate{
										ActiveKey: &hcpsdk20260901preview.KmsKeyUpdate{
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

			if devEnv {
				firstRotationFingerprint := backup.AzureKMSKeyFingerprint(clusterParams.KeyVaultName, clusterParams.EtcdEncryptionKeyName, firstKeyVersion)
				verifyOnDemandBackupRespectingPauseState(ctx, httpClient, adminAPIAddress, hcpResourceID, firstRotationFingerprint, "first")
			}

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
			updateResult, err = framework.UpdateHCPCluster20260901(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
						Etcd: &hcpsdk20260901preview.EtcdProfileUpdate{
							DataEncryption: &hcpsdk20260901preview.EtcdDataEncryptionProfileUpdate{
								CustomerManaged: &hcpsdk20260901preview.CustomerManagedEncryptionProfileUpdate{
									Kms: &hcpsdk20260901preview.KmsEncryptionProfileUpdate{
										ActiveKey: &hcpsdk20260901preview.KmsKeyUpdate{
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

			if devEnv {
				secondRotationFingerprint := backup.AzureKMSKeyFingerprint(clusterParams.KeyVaultName, clusterParams.EtcdEncryptionKeyName, secondKeyVersion)
				verifyOnDemandBackupRespectingPauseState(ctx, httpClient, adminAPIAddress, hcpResourceID, secondRotationFingerprint, "second")

				By("verifying backup schedules still exist after rotation")
				Eventually(func() (bool, error) {
					resp, err := getBackupScheduleViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
					if err != nil {
						return false, err
					}
					return len(resp.Schedules) > 0, nil
				}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
					"backup schedules should still exist after key rotation")
			}

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

// verifyOnDemandBackupRespectingPauseState checks the current backup schedule
// state and asserts accordingly: no backup if paused, otherwise the normal
// on-demand backup flow. It never changes the pause state itself.
func verifyOnDemandBackupRespectingPauseState(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, fingerprint, rotationLabel string) {
	const (
		scheduleStateObservationTimeout  = 2 * time.Minute
		scheduleStateObservationInterval = 10 * time.Second
	)

	By(fmt.Sprintf("checking current backup schedule state before verifying %s rotation backup behavior", rotationLabel))
	// The admin handler leaves BackupExecutionState empty until a schedule's ReadDesire
	// has observed KubeContent, so wait for every schedule to report a concrete state
	// before deciding; otherwise an unobserved schedule could be mistaken for "not paused".
	var schedResp hcp.BackupScheduleResponse
	Eventually(func() (bool, error) {
		resp, err := getBackupScheduleViaAdminAPI(ctx, httpClient, adminAPIAddress, resourceID)
		if err != nil {
			return false, err
		}
		schedResp = resp
		return allSchedulesHaveConcreteState(resp.Schedules), nil
	}, scheduleStateObservationTimeout, scheduleStateObservationInterval).Should(BeTrue(),
		"every backup schedule should report a concrete execution state before evaluating pause behavior")

	// State reflects only the per-cluster toggle; a fleet-wide pause leaves it
	// Enabled but reports every schedule's BackupExecutionState as Paused, so
	// check both to avoid waiting for a backup that will never be created.
	if schedResp.State == coreapi.BackupScheduleStateDisabled || allSchedulesPaused(schedResp.Schedules) {
		verifyNoOnDemandBackupForFingerprint(ctx, httpClient, adminAPIAddress, resourceID, fingerprint, rotationLabel)
		return
	}
	verifyOnDemandBackupForFingerprint(ctx, httpClient, adminAPIAddress, resourceID, fingerprint, rotationLabel)
}

// allSchedulesHaveConcreteState reports whether every schedule has observed a
// non-empty BackupExecutionState, used to avoid deciding pause state from a
// schedule whose ReadDesire hasn't observed KubeContent yet.
func allSchedulesHaveConcreteState(schedules []hcp.BackupScheduleDetail) bool {
	if len(schedules) == 0 {
		return false
	}
	for _, s := range schedules {
		if s.BackupExecutionState == "" {
			return false
		}
	}
	return true
}

// allSchedulesPaused reports whether every schedule is paused, used to detect a
// fleet-wide pause that the per-cluster State field alone would miss.
func allSchedulesPaused(schedules []hcp.BackupScheduleDetail) bool {
	if len(schedules) == 0 {
		return false
	}
	for _, s := range schedules {
		if s.BackupExecutionState != hcp.BackupExecutionStatePaused {
			return false
		}
	}
	return true
}

// verifyOnDemandBackupForFingerprint waits for an on-demand backup carrying the
// given KMS key fingerprint to appear via the admin API, e.g. after a key rotation.
func verifyOnDemandBackupForFingerprint(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, fingerprint, rotationLabel string) {
	By(fmt.Sprintf("verifying on-demand backup was created after %s rotation", rotationLabel))
	Eventually(func() (bool, error) {
		resp, err := getOnDemandBackupsViaAdminAPI(ctx, httpClient, adminAPIAddress, resourceID)
		if err != nil {
			return false, err
		}
		for _, b := range resp.Backups {
			if b.KMSKeyFingerprint == fingerprint {
				GinkgoLogr.Info("Found on-demand backup with expected fingerprint",
					"backupName", b.Name,
					"phase", b.Phase,
					"fingerprint", b.KMSKeyFingerprint)
				return true, nil
			}
		}
		return false, nil
	}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
		fmt.Sprintf("on-demand backup with the new key fingerprint should be created after the %s rotation", rotationLabel))
}

// verifyNoOnDemandBackupForFingerprint asserts no on-demand backup with the given
// fingerprint appears, used when the rotation completes while backups are paused.
// Uses a short window (not the full BackupWaitTimeout) since a wrongly-created
// on-demand backup would show up almost immediately, not after minutes.
func verifyNoOnDemandBackupForFingerprint(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, fingerprint, rotationLabel string) {
	const (
		noBackupCheckDuration   = 2 * time.Minute
		noBackupCheckInterval   = 15 * time.Second
		maxTransientErrorBudget = 3
	)

	By(fmt.Sprintf("verifying no on-demand backup was created after the paused %s rotation", rotationLabel))
	var lastErr string
	blipBudget := maxTransientErrorBudget
	Consistently(func() (bool, error) {
		resp, err := getOnDemandBackupsViaAdminAPI(ctx, httpClient, adminAPIAddress, resourceID)
		if err != nil {
			// Unlike Eventually, Consistently fails immediately on any error from this
			// func, with no retry tolerance. Tolerate a bounded number of transient
			// admin API errors so a brief blip doesn't flake this negative assertion,
			// but a persistently failing API still fails the check once the budget runs out.
			if blipBudget > 0 {
				blipBudget--
				if msg := err.Error(); msg != lastErr {
					GinkgoLogr.Info("Transient error checking on-demand backups, tolerating", "err", msg, "remainingBudget", blipBudget)
					lastErr = msg
				}
				return true, nil
			}
			return false, fmt.Errorf("admin API error budget (%d) exhausted while checking on-demand backups: %w", maxTransientErrorBudget, err)
		}
		lastErr = ""
		for _, b := range resp.Backups {
			if b.KMSKeyFingerprint == fingerprint {
				GinkgoLogr.Info("Unexpected on-demand backup found for fingerprint while backup schedules were paused",
					"backupName", b.Name,
					"phase", b.Phase,
					"fingerprint", b.KMSKeyFingerprint)
				return false, nil
			}
		}
		return true, nil
	}, noBackupCheckDuration, noBackupCheckInterval).Should(BeTrue(),
		fmt.Sprintf("no on-demand backup with the %s rotation's key fingerprint should be created while backup schedules are paused", rotationLabel))
}
