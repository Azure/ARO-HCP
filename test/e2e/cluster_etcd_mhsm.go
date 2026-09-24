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
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	hcpsdk20261001preview "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Cluster Etcd Managed HSM Encryption", func() {
	It("should create a cluster with Managed HSM keyVaultType and rotate the etcd KMS key",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "etcd-mhsm"

			tc := framework.NewTestContext()

			By("probing v20261001preview API availability before creating any Azure resources")
			probePager := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListBySubscriptionPager(nil)
			_, probeErr := probePager.NextPage(ctx)
			if framework.IsAPINotDeployedError(probeErr) {
				if time.Now().Before(framework.V20261001PreviewDeploymentDeadline) {
					Skip(fmt.Sprintf("v20261001preview API not yet deployed; skipping until %s", framework.V20261001PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20261001preview API still not deployed as of %s deadline", framework.V20261001PreviewDeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(probeErr).NotTo(HaveOccurred(), "failed to probe v20261001preview API availability")

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "etcd-mhsm", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for etcd mHSM test")

			By("generating security domain quorum certificates for Managed HSM activation")
			certDir := GinkgoT().TempDir()
			cert0Base64, cert1Base64, cert2Base64, err := generateMHSMSecurityDomainCertificates(certDir)
			Expect(err).NotTo(HaveOccurred(), "failed to generate mHSM security domain certificates")

			By("resolving the deploying principal for Managed HSM admin access")
			deployerIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve deploying principal identity")
			Expect(deployerIdentity.ObjectID).NotTo(BeEmpty(), "deploying principal object ID was empty")

			By("deploying Managed HSM with activation and initial key creation")
			hsmName := fmt.Sprintf("mhsm-%s", utilrand.String(12))
			keyName := "etcd-cmk"
			mhsmDeploymentName := fmt.Sprintf("mhsm-deploy-%s", customerClusterName)

			mhsmDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/managed-hsm-bootstrap.json"),
				framework.WithDeploymentName(mhsmDeploymentName),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"hsmName":                    hsmName,
					"keyName":                    keyName,
					"location":                   tc.Location(),
					"wrappingCertificate0Base64": cert0Base64,
					"wrappingCertificate1Base64": cert1Base64,
					"wrappingCertificate2Base64": cert2Base64,
					"securityDomainQuorum":       2,
					"additionalAdminObjectIds":   []interface{}{deployerIdentity.ObjectID},
					"keyManagerObjectIds":        []interface{}{},
					"enablePurgeProtection":      false,
					"softDeleteRetentionInDays":  7,
					"keySize":                    3072,
					"forceUpdateTag":             "test-run-1",
				}),
				framework.WithTimeout(60*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy and activate Managed HSM")

			keyID, err := framework.GetOutputValueString(mhsmDeploymentResult, "keyId")
			Expect(err).NotTo(HaveOccurred(), "failed to read keyId from mHSM deployment outputs")
			Expect(keyID).NotTo(BeEmpty(), "keyId was empty")

			GinkgoLogr.Info("Managed HSM deployed and activated successfully",
				"hsmName", hsmName,
				"keyName", keyName,
				"keyID", keyID)

			By("parsing key version from Managed HSM key ID")
			keyVersion, keyVaultName, err := parseMHSMKeyIDComponents(keyID)
			Expect(err).NotTo(HaveOccurred(), "failed to parse key version from key ID %q", keyID)

			// grantMHSMCryptoUser grants a principal the "Managed HSM Crypto User" role at the given
			// mHSM data-plane scope. mHSM has its own per-HSM data-plane RBAC, so a subscription-scope
			// grant cannot cover this HSM.
			grantMHSMCryptoUser := func(principalID, scope string) {
				assignRoleCmd := exec.CommandContext(ctx, "az", "keyvault", "role", "assignment", "create",
					"--hsm-name", hsmName,
					"--assignee-object-id", principalID,
					"--assignee-principal-type", "ServicePrincipal",
					"--role", "Managed HSM Crypto User",
					"--scope", scope,
					"--only-show-errors",
					"--output", "none")
				assignOutput, err := assignRoleCmd.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), "failed to grant Managed HSM Crypto User role to %s at %s: %s", principalID, scope, string(assignOutput))
				GinkgoLogr.Info("Granted Managed HSM Crypto User role", "principalID", principalID, "scope", scope)
			}

			By("granting the test principal Managed HSM Crypto User to create a second key version")
			grantMHSMCryptoUser(deployerIdentity.ObjectID, "/keys")

			By("creating a second key version in the Managed HSM for the rotation")
			mhsmURL := fmt.Sprintf("https://%s.managedhsm.azure.net/", hsmName)
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential for Managed HSM key client")
			keyClient, err := azkeys.NewClient(mhsmURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Managed HSM key client")

			var secondKeyVersion string
			Eventually(func() error {
				createKeyResp, createErr := keyClient.CreateKey(ctx, keyName, azkeys.CreateKeyParameters{
					Kty:     to.Ptr(azkeys.KeyTypeRSAHSM),
					KeySize: to.Ptr(int32(3072)),
					KeyOps:  []*azkeys.KeyOperation{to.Ptr(azkeys.KeyOperationEncrypt), to.Ptr(azkeys.KeyOperationDecrypt)},
				}, nil)
				if createErr != nil {
					return createErr
				}
				if createKeyResp.Key == nil || createKeyResp.Key.KID == nil {
					return fmt.Errorf("created key response was missing key ID")
				}
				secondKeyVersion = createKeyResp.Key.KID.Version()
				return nil
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "failed to create second Managed HSM key version (mHSM RBAC may still be propagating)")
			Expect(secondKeyVersion).NotTo(BeEmpty(), "second key version was empty")
			Expect(secondKeyVersion).NotTo(Equal(keyVersion), "second key version should differ from the initial version")
			GinkgoLogr.Info("Created second Managed HSM key version for rotation",
				"keyName", keyName,
				"secondKeyVersion", secondKeyVersion)

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for etcd mHSM cluster")

			By("granting Managed HSM Crypto User role on the key to the runtime KMS caller(s)")
			Expect(clusterParams.UserAssignedIdentitiesProfile).NotTo(BeNil(), "cluster params UserAssignedIdentitiesProfile was nil")
			kmsIdentityResourceID := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators[framework.KmsMiName]
			Expect(kmsIdentityResourceID).NotTo(BeNil(), "KMS control plane operator identity resource ID was nil")
			kmsIdentityPrincipalID, err := tc.GetUserAssignedIdentityPrincipalID(ctx, *kmsIdentityResourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve KMS identity principal ID")
			Expect(kmsIdentityPrincipalID).NotTo(BeEmpty(), "KMS identity principalId was empty")

			// Grant the per-cluster KMS managed identity — the real runtime caller in environments with a
			// live Managed Identities Data Plane (stage/prod). In dev/CI that dataplane is mocked, so every
			// operator (including the KMS plugin) authenticates as the MSI mock service principal instead;
			// there we must also grant that principal (MI_MOCK_PRINCIPAL_ID) or the encrypt call gets 403.
			grantMHSMCryptoUser(kmsIdentityPrincipalID, fmt.Sprintf("/keys/%s", keyName))
			if miMockPrincipalID := framework.MIMockPrincipalID(); miMockPrincipalID != "" {
				grantMHSMCryptoUser(miMockPrincipalID, fmt.Sprintf("/keys/%s", keyName))
			}

			clusterParams.EtcdEncryptionKeyName = keyName
			clusterParams.EtcdEncryptionKeyVersion = keyVersion
			clusterParams.KeyVaultName = keyVaultName
			clusterParams.KeyVaultType = string(hcpsdk20261001preview.KmsKeyVaultTypeManagedHSM)

			By("creating HCP cluster with Managed HSM keyVaultType via v20261001preview")
			err = tc.CreateHCPClusterFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with mHSM keyVaultType", customerClusterName)

			By("verifying keyVaultType was stored correctly via GET")
			cluster, err := framework.GetHCPCluster20261001(ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster %q after creation", customerClusterName)
			Expect(cluster.Properties).NotTo(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Etcd).NotTo(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType).NotTo(BeNil(), "cluster %q Kms.KeyVaultType was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType).To(Equal(hcpsdk20261001preview.KmsKeyVaultTypeManagedHSM),
				"cluster %q keyVaultType should be ManagedHSM", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for mHSM cluster %q", customerClusterName)

			By("verifying the cluster is viable (etcd encryption with mHSM key is functional)")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster viability check failed — etcd encryption with mHSM key should be working")

			By("rotating the cluster to the second Managed HSM key version")
			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			updateResult, err := framework.UpdateHCPCluster20261001(ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				hcpsdk20261001preview.HcpOpenShiftCluster{
					Properties: &hcpsdk20261001preview.HcpOpenShiftClusterProperties{
						Etcd: &hcpsdk20261001preview.EtcdProfile{
							DataEncryption: &hcpsdk20261001preview.EtcdDataEncryptionProfile{
								CustomerManaged: &hcpsdk20261001preview.CustomerManagedEncryptionProfile{
									Kms: &hcpsdk20261001preview.KmsEncryptionProfile{
										ActiveKey: &hcpsdk20261001preview.KmsKey{
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
			Expect(err).NotTo(HaveOccurred(), "failed to rotate cluster %q to the second mHSM key version", customerClusterName)
			Expect(updateResult.Properties).NotTo(BeNil(), "rotation result Properties was nil")
			Expect(updateResult.Properties.Etcd).NotTo(BeNil(), "rotation result Etcd was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption).NotTo(BeNil(), "rotation result DataEncryption was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "rotation result CustomerManaged was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "rotation result Kms was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "rotation result ActiveKey was nil")
			Expect(updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "rotation result key Version was nil")
			Expect(*updateResult.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(secondKeyVersion),
				"cluster %q should reference the second key version after rotation", customerClusterName)

			By("confirming the second key version persists via GET (round-trip verification)")
			rotatedCluster, err := framework.GetHCPCluster20261001(ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster %q after rotation", customerClusterName)
			Expect(rotatedCluster.Properties).NotTo(BeNil(), "rotated cluster Properties was nil")
			Expect(rotatedCluster.Properties.Etcd).NotTo(BeNil(), "rotated cluster Etcd was nil")
			Expect(rotatedCluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "rotated cluster DataEncryption was nil")
			Expect(rotatedCluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "rotated cluster CustomerManaged was nil")
			Expect(rotatedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "rotated cluster Kms was nil")
			Expect(rotatedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey).NotTo(BeNil(), "rotated cluster ActiveKey was nil")
			Expect(rotatedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).NotTo(BeNil(), "rotated cluster key Version was nil")
			Expect(*rotatedCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version).To(Equal(secondKeyVersion),
				"cluster %q should reference the second key version after round-trip GET", customerClusterName)

			By("verifying etcd re-encryption (StorageVersionMigration) succeeded after rotation")
			err = verifiers.VerifyStorageVersionMigrationSucceeded().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "StorageVersionMigration should reach Succeeded after mHSM key rotation")

			By("verifying the cluster remains viable after rotation")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyStorageVersionMigrationSucceeded())
			Expect(err).NotTo(HaveOccurred(), "cluster should remain viable after mHSM key rotation")

			GinkgoLogr.Info("Cluster with Managed HSM etcd encryption rotated successfully",
				"clusterName", customerClusterName,
				"hsmName", hsmName,
				"initialKeyVersion", keyVersion,
				"rotatedKeyVersion", secondKeyVersion)
		},
	)
})

func parseMHSMKeyIDComponents(keyID string) (version, vaultName string, err error) {
	// mHSM key ID format: https://<vault-name>.managedhsm.azure.net/keys/<key-name>/<version>
	u, err := url.Parse(keyID)
	if err != nil {
		return "", "", fmt.Errorf("parsing key ID %q: %w", keyID, err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "keys" {
		return "", "", fmt.Errorf("unexpected key ID path %q: want /keys/<name>/<version>", u.Path)
	}
	version = parts[2]
	dotIdx := strings.IndexByte(u.Hostname(), '.')
	if dotIdx < 0 {
		return "", "", fmt.Errorf("unexpected hostname %q in key ID %q", u.Hostname(), keyID)
	}
	vaultName = u.Hostname()[:dotIdx]
	return version, vaultName, nil
}

func generateMHSMSecurityDomainCertificates(certDir string) (cert0Base64, cert1Base64, cert2Base64 string, err error) {
	for i := 0; i < 3; i++ {
		privateKeyPath := fmt.Sprintf("%s/cert_%d.key", certDir, i)
		publicCertPath := fmt.Sprintf("%s/cert_%d.cer", certDir, i)

		cmd := exec.Command("openssl", "req",
			"-newkey", "rsa:3072",
			"-nodes",
			"-keyout", privateKeyPath,
			"-x509",
			"-sha256",
			"-days", "3650",
			"-subj", fmt.Sprintf("/CN=mhsm-security-domain-%d", i),
			"-out", publicCertPath)

		output, execErr := cmd.CombinedOutput()
		if execErr != nil {
			return "", "", "", fmt.Errorf("failed to generate certificate %d: %w (output: %s)", i, execErr, string(output))
		}

		if err := os.Chmod(privateKeyPath, 0600); err != nil {
			return "", "", "", fmt.Errorf("failed to chmod private key %d: %w", i, err)
		}
	}

	readAndEncode := func(path string) (string, error) {
		certBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("failed to read %s: %w", path, readErr)
		}
		return base64.StdEncoding.EncodeToString(certBytes), nil
	}

	cert0Base64, err = readAndEncode(fmt.Sprintf("%s/cert_0.cer", certDir))
	if err != nil {
		return "", "", "", err
	}

	cert1Base64, err = readAndEncode(fmt.Sprintf("%s/cert_1.cer", certDir))
	if err != nil {
		return "", "", "", err
	}

	cert2Base64, err = readAndEncode(fmt.Sprintf("%s/cert_2.cer", certDir))
	if err != nil {
		return "", "", "", err
	}

	return cert0Base64, cert1Base64, cert2Base64, nil
}
