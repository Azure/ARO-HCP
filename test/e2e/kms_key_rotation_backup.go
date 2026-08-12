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
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("HCP", func() {
	It("creates on-demand backup after KMS key rotation completes",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.MIContainers(1),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("checking API version availability")
			apiAvailable, err := tc.IsHCPAPIVersionAvailable(ctx, "2026-06-30-preview")
			Expect(err).NotTo(HaveOccurred(), "failed to check API version availability")
			if !apiAvailable {
				if time.Now().After(framework.V20260630PreviewDeploymentDeadline) {
					Fail(fmt.Sprintf("API version 2026-06-30-preview should be fully available by %s", framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Skip("API version 2026-06-30-preview is not fully available in this environment")
			}

			if tc.UsePooledIdentities() {
				err = tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			const clusterName = "kms-bkp-rotate"

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "kms-bkp-rotate", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters with version 4.22 and KMS")
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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20260630(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(framework.V20260630PreviewDeploymentDeadline) {
					Skip(fmt.Sprintf("v20260630preview API not yet deployed; skipping until %s", framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260630preview API still not deployed as of %s deadline", framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				metadataapi.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)

			By("creating admin API HTTP client")
			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
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

			By("rotating the KMS key")
			keyVaultURL := fmt.Sprintf("https://%s.vault.azure.net/", clusterParams.KeyVaultName)
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			keyClient, err := azkeys.NewClient(keyVaultURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Key Vault client")

			preRotationFingerprint := backup.AzureKMSKeyFingerprint(clusterParams.KeyVaultName, clusterParams.EtcdEncryptionKeyName, clusterParams.EtcdEncryptionKeyVersion)
			GinkgoLogr.Info("Pre-rotation fingerprint", "fingerprint", preRotationFingerprint)

			createKeyResp, err := keyClient.CreateKey(ctx, clusterParams.EtcdEncryptionKeyName, azkeys.CreateKeyParameters{
				Kty:     to.Ptr(azkeys.KeyTypeRSA),
				KeySize: to.Ptr(int32(2048)),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create new key version (rotation)")
			Expect(createKeyResp.Key).NotTo(BeNil(), "created key response was nil")
			Expect(createKeyResp.Key.KID).NotTo(BeNil(), "created key ID was nil")

			newKeyVersion := createKeyResp.Key.KID.Version()
			Expect(newKeyVersion).NotTo(BeEmpty(), "created key ID version was empty")

			postRotationFingerprint := backup.AzureKMSKeyFingerprint(clusterParams.KeyVaultName, clusterParams.EtcdEncryptionKeyName, newKeyVersion)
			GinkgoLogr.Info("Post-rotation fingerprint",
				"newVersion", newKeyVersion,
				"fingerprint", postRotationFingerprint)

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
											Version: to.Ptr(newKeyVersion),
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

			GinkgoLogr.Info("KMS key rotation completed",
				"clusterName", clusterName,
				"newKeyVersion", newKeyVersion,
				"provisioningState", *updateResult.Properties.ProvisioningState)

			By("verifying on-demand backup was created after rotation")
			Eventually(func() (bool, error) {
				resp, err := getOnDemandBackupsViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
				if err != nil {
					return false, err
				}
				for _, b := range resp.Backups {
					if b.KMSKeyFingerprint == postRotationFingerprint {
						GinkgoLogr.Info("Found on-demand backup with expected fingerprint",
							"backupName", b.Name,
							"phase", b.Phase,
							"fingerprint", b.KMSKeyFingerprint)
						return true, nil
					}
				}
				return false, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
				"on-demand backup with the new key fingerprint should be created after rotation")

			By("verifying backup schedules still exist after rotation")
			Eventually(func() (bool, error) {
				resp, err := getBackupScheduleViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
				if err != nil {
					return false, err
				}
				return len(resp.Schedules) > 0, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
				"backup schedules should still exist after key rotation")
		},
	)
})

func getOnDemandBackupsViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (hcp.OnDemandBackupResponse, error) {
	return framework.DoAdminAPIRequest[hcp.OnDemandBackupResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups", adminAPIAddress, resourceID), http.StatusOK, nil)
}
