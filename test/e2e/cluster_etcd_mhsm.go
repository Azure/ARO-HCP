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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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

			By("probing v20261001 API availability before creating any Azure resources")
			probePager := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListBySubscriptionPager(nil)
			_, probeErr := probePager.NextPage(ctx)
			Expect(probeErr).NotTo(HaveOccurred(), "failed to probe v20261001 API availability")

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "etcd-mhsm", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for etcd mHSM test")

			By("generating security domain quorum certificates for Managed HSM activation")
			certDir := GinkgoT().TempDir()
			wrappingCertPaths, err := generateMHSMSecurityDomainCertificates(certDir)
			Expect(err).NotTo(HaveOccurred(), "failed to generate mHSM security domain certificates")

			By("resolving the deploying principal for Managed HSM admin access")
			deployerIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve deploying principal identity")
			Expect(deployerIdentity.ObjectID).NotTo(BeEmpty(), "deploying principal object ID was empty")

			By("deploying an un-activated Managed HSM with the test principal as initial administrator")
			hsmName := fmt.Sprintf("mhsm-%s", utilrand.String(12))
			keyName := "etcd-cmk"
			mhsmDeploymentName := fmt.Sprintf("mhsm-deploy-%s", customerClusterName)

			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/managed-hsm-bootstrap.json"),
				framework.WithDeploymentName(mhsmDeploymentName),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"hsmName":                   hsmName,
					"location":                  tc.Location(),
					"adminObjectIds":            []interface{}{deployerIdentity.ObjectID},
					"enablePurgeProtection":     false,
					"softDeleteRetentionInDays": 7,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy Managed HSM")

			// Activation runs from the test process rather than an ARM deploymentScript, whose
			// Azure Container Instance intermittently never starts (AROSLSRE-2510). The download
			// returns before ARM reports the HSM as Active, so once a download has succeeded the
			// loop only polls the status instead of downloading the security domain again.
			By("activating the Managed HSM by downloading its security domain")
			// The HSM lives in the test's customer subscription, which may differ from the az CLI default.
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve test subscription ID")
			activationAttempt := 0
			securityDomainDownloaded := false
			Eventually(func() error {
				status, statusErr := mhsmActivationStatus(ctx, subscriptionID, *resourceGroup.Name, hsmName)
				if statusErr != nil {
					return statusErr
				}
				if status == mhsmActivationStatusActive {
					return nil
				}
				if securityDomainDownloaded {
					return fmt.Errorf("security domain downloaded; activation status is %q, waiting for %q", status, mhsmActivationStatusActive)
				}
				activationAttempt++
				securityDomainFile := filepath.Join(certDir, fmt.Sprintf("security-domain-%d.json", activationAttempt))
				if downloadErr := downloadMHSMSecurityDomain(ctx, hsmName, securityDomainFile, wrappingCertPaths, 2); downloadErr != nil {
					return fmt.Errorf("activation attempt %d (status %q): %w", activationAttempt, status, downloadErr)
				}
				securityDomainDownloaded = true
				return fmt.Errorf("security domain downloaded on attempt %d; waiting for activation status %q", activationAttempt, mhsmActivationStatusActive)
			}, 20*time.Minute, 30*time.Second).Should(Succeed(), "failed to activate Managed HSM %s", hsmName)
			GinkgoLogr.Info("Managed HSM activated", "hsmName", hsmName, "attempts", activationAttempt)

			// Detect the deployer's principal type so local User accounts and Prow's ServicePrincipal both work.
			deployerPrincipalType := "ServicePrincipal"
			if deployerIdentity.PrincipalType == framework.PrincipalTypeDSTSUser {
				deployerPrincipalType = "User"
			}

			// grantMHSMCryptoUser grants a principal the "Managed HSM Crypto User" role at the given
			// mHSM data-plane scope. mHSM has its own per-HSM data-plane RBAC, so a subscription-scope
			// grant cannot cover this HSM. Retried because the data plane of a freshly activated
			// HSM can briefly reject role assignments.
			grantMHSMCryptoUser := func(principalID, principalType, scope string) {
				Eventually(func() error {
					return ensureMHSMCryptoUser(ctx, hsmName, principalID, principalType, scope)
				}, 5*time.Minute, 15*time.Second).Should(Succeed(), "failed to grant Managed HSM Crypto User role to %s at %s", principalID, scope)
				GinkgoLogr.Info("Granted Managed HSM Crypto User role", "principalID", principalID, "scope", scope)
			}

			By("granting the test principal Managed HSM Crypto User to create key versions")
			grantMHSMCryptoUser(deployerIdentity.ObjectID, deployerPrincipalType, "/keys")

			mhsmURL := fmt.Sprintf("https://%s.managedhsm.azure.net/", hsmName)
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential for Managed HSM key client")
			keyClient, err := azkeys.NewClient(mhsmURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Managed HSM key client")

			createKeyVersion := func() string {
				var keyID string
				Eventually(func() error {
					createKeyResp, createErr := keyClient.CreateKey(ctx, keyName, azkeys.CreateKeyParameters{
						Kty:     to.Ptr(azkeys.KeyTypeRSAHSM),
						KeySize: to.Ptr(int32(3072)),
						KeyOps:  []*azkeys.KeyOperation{to.Ptr(azkeys.KeyOperationEncrypt), to.Ptr(azkeys.KeyOperationDecrypt), to.Ptr(azkeys.KeyOperationWrapKey), to.Ptr(azkeys.KeyOperationUnwrapKey)},
					}, nil)
					if createErr != nil {
						return createErr
					}
					if createKeyResp.Key == nil || createKeyResp.Key.KID == nil {
						return fmt.Errorf("created key response was missing key ID")
					}
					keyID = string(*createKeyResp.Key.KID)
					return nil
				}, 5*time.Minute, 15*time.Second).Should(Succeed(), "failed to create Managed HSM key version (mHSM RBAC may still be propagating)")
				Expect(keyID).NotTo(BeEmpty(), "created key ID was empty")
				return keyID
			}

			By("creating the initial key in the Managed HSM")
			keyID := createKeyVersion()
			GinkgoLogr.Info("Managed HSM key created",
				"hsmName", hsmName,
				"keyName", keyName,
				"keyID", keyID)

			By("parsing key version from Managed HSM key ID")
			keyVersion, keyVaultName, err := parseMHSMKeyIDComponents(keyID)
			Expect(err).NotTo(HaveOccurred(), "failed to parse key version from key ID %q", keyID)

			By("creating a second key version in the Managed HSM for the rotation")
			secondKeyVersion, _, err := parseMHSMKeyIDComponents(createKeyVersion())
			Expect(err).NotTo(HaveOccurred(), "failed to parse second key version")
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
			// KMS requires VNet integration; DisableSwift=false provides vnetIntegrationSubnetId (see test/e2e/README.md).
			clusterParams.DisableSwift = false

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
			grantMHSMCryptoUser(kmsIdentityPrincipalID, "ServicePrincipal", fmt.Sprintf("/keys/%s", keyName))
			if miMockPrincipalID := framework.MIMockPrincipalID(); miMockPrincipalID != "" {
				grantMHSMCryptoUser(miMockPrincipalID, "ServicePrincipal", fmt.Sprintf("/keys/%s", keyName))
			}

			clusterParams.EtcdEncryptionKeyName = keyName
			clusterParams.EtcdEncryptionKeyVersion = keyVersion
			clusterParams.KeyVaultName = keyVaultName
			clusterParams.KeyVaultType = string(hcpsdk20261001.KmsKeyVaultTypeManagedHSM)

			By("creating HCP cluster with Managed HSM keyVaultType via v20261001")
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
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType).To(Equal(hcpsdk20261001.KmsKeyVaultTypeManagedHSM),
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
				hcpsdk20261001.HcpOpenShiftCluster{
					Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
						Etcd: &hcpsdk20261001.EtcdProfile{
							DataEncryption: &hcpsdk20261001.EtcdDataEncryptionProfile{
								CustomerManaged: &hcpsdk20261001.CustomerManagedEncryptionProfile{
									Kms: &hcpsdk20261001.KmsEncryptionProfile{
										ActiveKey: &hcpsdk20261001.KmsKey{
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

const mhsmActivationStatusActive = "Active"

// mhsmActivationStatus returns the security-domain activation status of a Managed HSM
// (for example NotActivated or Active) as reported by ARM.
func mhsmActivationStatus(ctx context.Context, subscriptionID, resourceGroup, hsmName string) (string, error) {
	cmd := exec.CommandContext(ctx, "az", "keyvault", "show",
		"--subscription", subscriptionID,
		"--resource-group", resourceGroup,
		"--hsm-name", hsmName,
		"--query", "properties.securityDomainProperties.activationStatus",
		"--only-show-errors",
		"--output", "tsv")
	output, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = string(exitErr.Stderr)
		}
		return "", fmt.Errorf("failed to read activation status of Managed HSM %s: %w: %s", hsmName, err, stderr)
	}
	return strings.TrimSpace(string(output)), nil
}

// downloadMHSMSecurityDomain activates a Managed HSM by downloading its security domain
// encrypted with the given wrapping certificates. The caller must be an initial administrator.
func downloadMHSMSecurityDomain(ctx context.Context, hsmName, securityDomainFile string, wrappingCertPaths []string, quorum int) error {
	args := []string{"keyvault", "security-domain", "download",
		"--hsm-name", hsmName,
		"--security-domain-file", securityDomainFile,
		"--sd-quorum", strconv.Itoa(quorum),
		"--sd-wrapping-keys"}
	args = append(args, wrappingCertPaths...)
	args = append(args, "--only-show-errors", "--output", "none")
	output, err := exec.CommandContext(ctx, "az", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security domain download failed: %w: %s", err, string(output))
	}
	return nil
}

// ensureMHSMCryptoUser assigns "Managed HSM Crypto User" to a principal at the given data-plane
// scope unless an assignment already exists, so it is safe to retry.
func ensureMHSMCryptoUser(ctx context.Context, hsmName, principalID, principalType, scope string) error {
	listOutput, err := exec.CommandContext(ctx, "az", "keyvault", "role", "assignment", "list",
		"--hsm-name", hsmName,
		"--assignee-object-id", principalID,
		"--role", "Managed HSM Crypto User",
		"--scope", scope,
		"--only-show-errors",
		"--output", "json").Output()
	var existing []json.RawMessage
	if err == nil && json.Unmarshal(listOutput, &existing) == nil && len(existing) > 0 {
		return nil
	}

	createOutput, err := exec.CommandContext(ctx, "az", "keyvault", "role", "assignment", "create",
		"--hsm-name", hsmName,
		"--assignee-object-id", principalID,
		"--assignee-principal-type", principalType,
		"--role", "Managed HSM Crypto User",
		"--scope", scope,
		"--only-show-errors",
		"--output", "none").CombinedOutput()
	if err != nil {
		// A previous attempt may have created the assignment after the list above was served.
		if strings.Contains(string(createOutput), "MatchingRoleAssignmentExists") {
			return nil
		}
		return fmt.Errorf("role assignment create failed: %w: %s", err, string(createOutput))
	}
	return nil
}

// generateMHSMSecurityDomainCertificates writes three self-signed wrapping certificates
// (and their private keys) to certDir and returns the certificate paths.
func generateMHSMSecurityDomainCertificates(certDir string) ([]string, error) {
	certPaths := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		privateKeyPath := filepath.Join(certDir, fmt.Sprintf("cert_%d.key", i))
		publicCertPath := filepath.Join(certDir, fmt.Sprintf("cert_%d.cer", i))

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
			return nil, fmt.Errorf("failed to generate certificate %d: %w (output: %s)", i, execErr, string(output))
		}

		if err := os.Chmod(privateKeyPath, 0600); err != nil {
			return nil, fmt.Errorf("failed to chmod private key %d: %w", i, err)
		}
		certPaths = append(certPaths, publicCertPath)
	}

	return certPaths, nil
}
