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

	hcpsdk20261001preview "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Cluster Etcd Managed HSM Encryption", func() {
	It("should create a cluster with Managed HSM keyVaultType and verify etcd encryption works",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.ManualOnly,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "etcd-mhsm"

			tc := framework.NewTestContext()

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

			keyIDRaw, ok := mhsmDeploymentResult.Properties.Outputs.(map[string]interface{})["keyId"]
			Expect(ok).To(BeTrue(), "mHSM deployment output missing keyId")
			keyIDMap, ok := keyIDRaw.(map[string]interface{})
			Expect(ok).To(BeTrue(), "keyId output was not a map")
			keyID, ok := keyIDMap["value"].(string)
			Expect(ok).To(BeTrue(), "keyId value was not a string")
			Expect(keyID).NotTo(BeEmpty(), "keyId was empty")

			GinkgoLogr.Info("Managed HSM deployed and activated successfully",
				"hsmName", hsmName,
				"keyName", keyName,
				"keyID", keyID)

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
			// mHSM has its own per-HSM data-plane RBAC, so a subscription-scope grant cannot cover this HSM.
			grantMHSMCryptoUser := func(principalID string) {
				assignRoleCmd := exec.CommandContext(ctx, "az", "keyvault", "role", "assignment", "create",
					"--hsm-name", hsmName,
					"--assignee-object-id", principalID,
					"--assignee-principal-type", "ServicePrincipal",
					"--role", "Managed HSM Crypto User",
					"--scope", fmt.Sprintf("/keys/%s", keyName),
					"--only-show-errors",
					"--output", "none")
				assignOutput, err := assignRoleCmd.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), "failed to grant Managed HSM Crypto User role to %s: %s", principalID, string(assignOutput))
				GinkgoLogr.Info("Granted Managed HSM Crypto User role", "principalID", principalID, "keyName", keyName)
			}

			grantMHSMCryptoUser(kmsIdentityPrincipalID)
			if miMockPrincipalID := framework.MIMockPrincipalID(); miMockPrincipalID != "" {
				grantMHSMCryptoUser(miMockPrincipalID)
			}

			By("parsing key version from Managed HSM key ID")
			keyVersion, keyVaultName, err := parseMHSMKeyIDComponents(keyID)
			Expect(err).NotTo(HaveOccurred(), "failed to parse key version from key ID %q", keyID)
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

			GinkgoLogr.Info("Cluster with Managed HSM etcd encryption is fully operational",
				"clusterName", customerClusterName,
				"hsmName", hsmName,
				"keyID", keyID)
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
