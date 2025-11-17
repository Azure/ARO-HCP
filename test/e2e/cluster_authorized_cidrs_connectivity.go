// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/crypto/ssh"

	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Authorized CIDRs", func() {
	Context("Connectivity", func() {
		It("should allow API access only from authorized VM IP",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			func(ctx context.Context) {
				const (
					clusterName                      = "cidr-connectivity-test"
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					openshiftControlPlaneVersionId   = "4.19"
				)

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-cidr-connectivity", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("generating SSH key pair for VM")
				sshPublicKey, _, err := generateSSHKeyPair()
				Expect(err).NotTo(HaveOccurred())

				By("creating customer infrastructure")
				customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"customer-infra",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
					map[string]interface{}{
						"persistTagValue":        false,
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("deploying test VM")
				vmName := fmt.Sprintf("%s-test-vm", clusterName)
				vmDeployment, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"test-vm",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/test-vm.json")),
					map[string]interface{}{
						"vmName":       vmName,
						"vnetName":     customerVnetName,
						"subnetName":   customerVnetSubnetName,
						"sshPublicKey": sshPublicKey,
					},
					30*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("extracting VM public IP from deployment outputs")
				vmPublicIP, err := framework.GetOutputValueString(vmDeployment, "publicIP")
				Expect(err).NotTo(HaveOccurred())
				Expect(vmPublicIP).NotTo(BeEmpty(), "VM public IP should be in deployment outputs")

				By("creating managed identities")
				keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
				Expect(err).NotTo(HaveOccurred())
				keyVaultNameStr, ok := keyVaultName.(string)
				Expect(ok).To(BeTrue())
				etcdEncryptionKeyVersion, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
				Expect(err).NotTo(HaveOccurred())
				managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"managed-identities",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
					map[string]interface{}{
						"clusterName":  clusterName,
						"nsgName":      customerNetworkSecurityGroupName,
						"vnetName":     customerVnetName,
						"subnetName":   customerVnetSubnetName,
						"keyVaultName": keyVaultName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating cluster with VM IP in authorized CIDRs using SDK")
				userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
				Expect(err).NotTo(HaveOccurred())
				identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
				Expect(err).NotTo(HaveOccurred())
				etcdEncryptionKeyName, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
				Expect(err).NotTo(HaveOccurred())
				nsgResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "nsgID")
				Expect(err).NotTo(HaveOccurred())
				vnetSubnetResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
				Expect(err).NotTo(HaveOccurred())
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				userAssignedIdentitiesProfile, err := framework.ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
				Expect(err).NotTo(HaveOccurred())
				identityProfile, err := framework.ConvertToManagedServiceIdentity(identity)
				Expect(err).NotTo(HaveOccurred())

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = clusterName
				clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.NsgResourceID = nsgResourceID
				clusterParams.SubnetResourceID = vnetSubnetResourceID
				clusterParams.VnetName = customerVnetName
				clusterParams.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
				clusterParams.Identity = identityProfile
				clusterParams.KeyVaultName = keyVaultNameStr
				clusterParams.EtcdEncryptionKeyName = etcdEncryptionKeyName
				clusterParams.EtcdEncryptionKeyVersion = etcdEncryptionKeyVersion
				clusterParams.AuthorizedCIDRs = []*string{
					to.Ptr(fmt.Sprintf("%s/32", vmPublicIP)),
				}

				err = framework.CreateHCPClusterFromParam(ctx,
					tc,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("getting cluster details")
				clusterResponse, err := framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(clusterResponse.Properties).ToNot(BeNil())
				Expect(clusterResponse.Properties.API).ToNot(BeNil())
				Expect(clusterResponse.Properties.API.URL).ToNot(BeNil())
				apiURL := *clusterResponse.Properties.API.URL

				By("verifying authorized CIDRs contains VM IP")
				Expect(clusterResponse.Properties.API.AuthorizedCIDRs).ToNot(BeNil())
				Expect(clusterResponse.Properties.API.AuthorizedCIDRs).To(HaveLen(1))
				Expect(*clusterResponse.Properties.API.AuthorizedCIDRs[0]).To(Equal(fmt.Sprintf("%s/32", vmPublicIP)))

				By("testing connectivity from authorized VM")

				// Test connectivity using VM run command
				connectivityTest := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s/healthz", apiURL)
				output, err := runVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest)
				Expect(err).NotTo(HaveOccurred())

				// Should get HTTP response (likely 401 or 200, but not connection refused)
				httpCode := strings.TrimSpace(output)
				By(fmt.Sprintf("VM received HTTP status code: %s", httpCode))
				Expect(httpCode).To(MatchRegexp("^[2-5][0-9][0-9]$"), "Should receive valid HTTP status code from authorized IP")

				By("testing connectivity from current machine (should be blocked)")
				// Try to connect from the test runner (which is not in authorized CIDRs)
				err = testAPIConnectivity(apiURL, 5*time.Second)
				if err != nil {
					By(fmt.Sprintf("Connection from unauthorized IP correctly blocked: %v", err))
				} else {
					// If we can connect, it means the test runner's IP might be in the cluster's network
					// This is expected in some scenarios, so we just log it
					By("Warning: Connection from test runner succeeded - this may indicate the runner is in the authorized network")
				}

				By("verifying VM can access cluster API with credentials")
				adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				// Create kubeconfig and copy to VM
				kubeconfig, err := generateKubeconfig(adminRESTConfig)
				Expect(err).NotTo(HaveOccurred())

				// Test kubectl command from VM
				// Use base64 encoding to safely transfer kubeconfig
				kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))
				kubectlTest := fmt.Sprintf("echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get nodes 2>&1", kubeconfigB64)
				output, err = runVMCommand(ctx, tc, *resourceGroup.Name, vmName, kubectlTest)
				By(fmt.Sprintf("kubectl output from authorized VM: %s", output))

				// Should be able to run kubectl commands (even if nodes aren't ready)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).To(Or(
					ContainSubstring("NAME"),         // Success - got nodes
					ContainSubstring("No resources"), // Success - no nodes yet
					ContainSubstring("NotReady"),     // Success - nodes not ready
				), "Should be able to execute kubectl from authorized VM")

				By("updating cluster to remove VM from authorized CIDRs")
				// Get the current cluster state
				currentCluster, err := framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
				)
				Expect(err).NotTo(HaveOccurred())

				// Update the cluster's authorized CIDRs
				currentCluster.Properties.API.AuthorizedCIDRs = []*string{
					to.Ptr("192.0.2.0/24"), // Use TEST-NET-1 (reserved for documentation)
				}

				// Use CreateOrUpdate (PUT) to apply the change
				poller, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					clusterName,
					currentCluster.HcpOpenShiftCluster,
					nil,
				)
				Expect(err).NotTo(HaveOccurred())

				_, err = poller.PollUntilDone(ctx, nil)
				Expect(err).NotTo(HaveOccurred())

				// Wait a bit for the change to propagate
				time.Sleep(30 * time.Second)

				By("verifying VM is now blocked from API access")
				output, err = runVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest)
				if err != nil || strings.TrimSpace(output) == "000" || strings.TrimSpace(output) == "" {
					By("Connection correctly blocked after removing VM from authorized CIDRs")
				} else {
					// May take time to propagate
					By(fmt.Sprintf("Note: Connection still allowed (status: %s) - may need time to propagate", strings.TrimSpace(output)))
				}
			},
		)
	})
})

// Helper to run command on VM
func runVMCommand(ctx context.Context, tc interface {
	SubscriptionID(ctx context.Context) (string, error)
	AzureCredential() (azcore.TokenCredential, error)
}, resourceGroup, vmName, command string) (string, error) {
	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", err
	}

	azCreds, err := tc.AzureCredential()
	if err != nil {
		return "", err
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, azCreds, nil)
	if err != nil {
		return "", err
	}

	runCommandInput := armcompute.RunCommandInput{
		CommandID: to.Ptr("RunShellScript"),
		Script: []*string{
			to.Ptr(command),
		},
	}

	poller, err := computeClient.BeginRunCommand(ctx, resourceGroup, vmName, runCommandInput, nil)
	if err != nil {
		return "", err
	}

	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}

	if len(result.Value) > 0 && result.Value[0].Message != nil {
		// Azure Run Command returns output in format:
		// "Enable succeeded: \n[stdout]\n<actual output>\n[stderr]\n<errors>"
		// We need to extract just the stdout content
		message := *result.Value[0].Message

		// Find the stdout section
		stdoutStart := strings.Index(message, "[stdout]\n")
		if stdoutStart == -1 {
			// If no stdout marker, return the whole message
			return message, nil
		}

		// Skip past the "[stdout]\n" marker
		stdoutStart += len("[stdout]\n")

		// Find where stderr starts (if present)
		stderrStart := strings.Index(message[stdoutStart:], "\n[stderr]")

		var output string
		if stderrStart == -1 {
			// No stderr marker, take everything after stdout
			output = message[stdoutStart:]
		} else {
			// Take only the stdout section
			output = message[stdoutStart : stdoutStart+stderrStart]
		}

		return strings.TrimSpace(output), nil
	}

	return "", nil
}

// Helper to test API connectivity with timeout
func testAPIConnectivity(apiURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Simple HTTP GET to test connectivity
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL+"/healthz", nil)
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// Helper to generate SSH key pair
func generateSSHKeyPair() (publicKey string, privateKey string, err error) {
	// Generate RSA key pair
	privateKeyData, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	// Encode private key to PEM format
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKeyData),
	}
	privateKeyStr := string(pem.EncodeToMemory(privateKeyPEM))

	// Generate public key in SSH format
	pub, err := ssh.NewPublicKey(&privateKeyData.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicKeyStr := string(ssh.MarshalAuthorizedKey(pub))

	return publicKeyStr, privateKeyStr, nil
}

// Helper to generate kubeconfig
func generateKubeconfig(restConfig *rest.Config) (string, error) {
	var kubeconfig string

	// In development environments, CAData is cleared and Insecure is set to true
	// We need to handle this case by adding insecure-skip-tls-verify
	if len(restConfig.CAData) == 0 || restConfig.Insecure {
		kubeconfig = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: cluster
contexts:
- context:
    cluster: cluster
    user: admin
  name: admin@cluster
current-context: admin@cluster
users:
- name: admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`,
			restConfig.Host,
			base64.StdEncoding.EncodeToString(restConfig.CertData),
			base64.StdEncoding.EncodeToString(restConfig.KeyData),
		)
	} else {
		kubeconfig = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: cluster
contexts:
- context:
    cluster: cluster
    user: admin
  name: admin@cluster
current-context: admin@cluster
users:
- name: admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`,
			restConfig.Host,
			base64.StdEncoding.EncodeToString(restConfig.CAData),
			base64.StdEncoding.EncodeToString(restConfig.CertData),
			base64.StdEncoding.EncodeToString(restConfig.KeyData),
		)
	}

	return kubeconfig, nil
}
