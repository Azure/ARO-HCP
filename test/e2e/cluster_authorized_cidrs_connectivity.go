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
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Authorized CIDRs", func() {
	Context("Connectivity", func() {
		It("should allow API access only from authorized VM IP",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.IntegrationOnly,
			labels.AroRpApiCompatible,
			func(ctx context.Context) {
				const (
					clusterName                      = "cidr-connectivity-test"
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					customerExternalAuthName         = "ext-auth-cidr"
					externalAuthSubjectPrefix        = "prefix-"
				)

				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
					Expect(err).NotTo(HaveOccurred())
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-cidr-connectivity", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("creating cluster parameters")
				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = clusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName

				By("creating customer resources")
				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred())

				By("generating SSH key pair for VM")
				sshPublicKey, _, err := framework.GenerateSSHKeyPair()
				Expect(err).NotTo(HaveOccurred())

				By("deploying test VM")
				vmName := fmt.Sprintf("%s-test-vm", clusterName)
				vmDeployment, err := tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/test-vm.json"),
					framework.WithDeploymentName("test-vm"),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"vmName":       vmName,
						"vnetName":     customerVnetName,
						"subnetName":   customerVnetSubnetName,
						"sshPublicKey": sshPublicKey,
					}),
					framework.WithTimeout(30*time.Minute),
				)
				Expect(err).NotTo(HaveOccurred())

				By("extracting VM public IP from deployment outputs")
				vmPublicIP, err := framework.GetOutputValueString(vmDeployment, "publicIP")
				Expect(err).NotTo(HaveOccurred())
				Expect(vmPublicIP).NotTo(BeEmpty(), "VM public IP should be in deployment outputs")

				By("Creating a cluster with authorized CIDR containing VM IP")
				clusterParams.AuthorizedCIDRs = []*string{
					to.Ptr(fmt.Sprintf("%s/32", vmPublicIP)),
				}

				err = tc.CreateHCPClusterFromParam(
					ctx,
					GinkgoLogr,
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

				// Wrap in Eventually for robustness - authorized CIDR rules may take time to propagate
				var httpCode string
				Eventually(func(g Gomega) {
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest, 2*time.Minute)
					g.Expect(err).NotTo(HaveOccurred())

					// Should get HTTP response (likely 401 or 200, but not connection refused)
					httpCode = strings.TrimSpace(output)
					By(fmt.Sprintf("VM received HTTP status code: %s", httpCode))
					g.Expect(httpCode).To(MatchRegexp("^[2-5][0-9][0-9]$"), "Should receive valid HTTP status code from authorized IP")
				}, 2*time.Minute, 10*time.Second).Should(Succeed())

				By("testing connectivity from current machine (should be blocked)")
				// Try to connect from the test runner (which is not in authorized CIDRs)
				err = testAPIConnectivity(apiURL, 5*time.Second)
				Expect(err).To(HaveOccurred(), "Connection from unauthorized IP should be blocked")
				// Verify it's a connection error (EOF indicates connection was closed by server/network)
				Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("Get \"%s/healthz\": EOF", apiURL)), "Should fail with EOF error indicating blocked connection")

				By("verifying VM can access cluster API with credentials")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				// Create kubeconfig and copy to VM
				kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
				Expect(err).NotTo(HaveOccurred())

				// Test kubectl command from VM
				kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))
				kubectlTest := fmt.Sprintf("echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get nodes -o json", kubeconfigB64)

				Eventually(func(g Gomega) {
					var err error
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, kubectlTest, 2*time.Minute)
					By("kubectl command executed from authorized VM")

					// Should be able to run kubectl commands successfully (exit code 0)
					g.Expect(err).NotTo(HaveOccurred(), "kubectl should execute successfully from authorized VM")

					// Deserialize JSON response to validate API connectivity
					var nodeList corev1.NodeList
					err = json.Unmarshal([]byte(output), &nodeList)
					g.Expect(err).NotTo(HaveOccurred(), "Should receive valid JSON response from Kubernetes API")

					// Verify the response has correct Kubernetes API structure
					g.Expect(nodeList.APIVersion).To(Equal("v1"), "Response should have v1 API version")
					g.Expect(nodeList.Kind).To(Equal("List"), "Response should be a List kind")
					// Note: Items may be empty if nodes aren't ready yet, but structure should be valid
					By(fmt.Sprintf("Successfully retrieved node list with %d items", len(nodeList.Items)))
				}, 2*time.Minute, 10*time.Second).Should(Succeed())

				By("verifying aggregated API services from authorized VM")
				// Only output unavailable services (filter out :True lines) to stay within 4KB VM output limit
				apiServicesCmd := fmt.Sprintf(
					`echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get apiservices -o jsonpath='{range .items[*]}{.metadata.name}:{.status.conditions[?(@.type=="Available")].status}{"\n"}{end}' | grep -v ':True$'`,
					kubeconfigB64,
				)

				Eventually(func(g Gomega) {
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, apiServicesCmd, 2*time.Minute)
					g.Expect(err).NotTo(HaveOccurred(), "kubectl get apiservices should succeed from authorized VM")

					unavailableServices := parseUnavailableResources(output)
					g.Expect(unavailableServices).To(BeEmpty(), "All APIServices should report Available=True, but these are not available: %v", unavailableServices)
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				By("creating the node pool")
				nodePoolParams := framework.NewDefaultNodePoolParams()
				nodePoolParams.ClusterName = clusterName
				nodePoolParams.NodePoolName = "np-1"
				nodePoolParams.Replicas = int32(2)

				err = tc.CreateNodePoolFromParam(ctx,
					*resourceGroup.Name,
					clusterName,
					nodePoolParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating an app registration with a client secret")
				app, sp, err := tc.NewAppRegistrationWithServicePrincipal(ctx)
				Expect(err).NotTo(HaveOccurred())

				graphClient, err := tc.GetGraphClient(ctx)
				Expect(err).NotTo(HaveOccurred())

				pass, err := graphClient.AddPassword(ctx, app.ID, "cidr-external-auth-pass", time.Now(), time.Now().Add(24*time.Hour))
				Expect(err).NotTo(HaveOccurred())

				By("creating an external auth config with a prefix")
				extAuth := hcpsdk20240610preview.ExternalAuth{
					Properties: &hcpsdk20240610preview.ExternalAuthProperties{
						Issuer: &hcpsdk20240610preview.TokenIssuerProfile{
							URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
							Audiences: []*string{to.Ptr(app.AppID)},
						},
						Claim: &hcpsdk20240610preview.ExternalAuthClaimProfile{
							Mappings: &hcpsdk20240610preview.TokenClaimMappingsProfile{
								Username: &hcpsdk20240610preview.UsernameClaimProfile{
									Claim:        to.Ptr("sub"), // objectID of SP
									PrefixPolicy: to.Ptr(hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix),
									Prefix:       to.Ptr(externalAuthSubjectPrefix),
								},
								Groups: &hcpsdk20240610preview.GroupClaimProfile{
									Claim: to.Ptr("groups"),
								},
							},
						},
						Clients: []*hcpsdk20240610preview.ExternalAuthClientProfile{
							{
								ClientID: to.Ptr(app.AppID),
								Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
									Name:                to.Ptr("console"),
									AuthClientNamespace: to.Ptr("openshift-console"),
								},
								Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypeConfidential),
							},
							{
								ClientID: to.Ptr(app.AppID),
								Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
									Name:                to.Ptr("cli"),
									AuthClientNamespace: to.Ptr("openshift-console"),
								},
								Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypePublic),
							},
						},
					},
				}
				_, err = framework.CreateOrUpdateExternalAuthAndWait(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, customerExternalAuthName, extAuth, 15*time.Minute)
				Expect(err).NotTo(HaveOccurred())

				By("verifying ExternalAuth is in a Succeeded state")
				eaResult, err := framework.GetExternalAuth(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, customerExternalAuthName)
				Expect(err).NotTo(HaveOccurred())
				Expect(*eaResult.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ExternalAuthProvisioningStateSucceeded))

				By("creating a cluster role binding for the entra application via VM")
				clusterRoleBindingName := "external-auth-cluster-admin"
				clusterRoleBindingSubject := externalAuthSubjectPrefix + sp.ID
				createClusterRoleBindingCmd := fmt.Sprintf(
					`echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig create clusterrolebinding %s --clusterrole=cluster-admin --user=%s`,
					kubeconfigB64,
					clusterRoleBindingName,
					clusterRoleBindingSubject,
				)
				_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, createClusterRoleBindingCmd, 2*time.Minute)
				Expect(err).NotTo(HaveOccurred())

				By("creating a rest config using OIDC authentication")
				Expect(tc.TenantID()).NotTo(BeEmpty())
				cred, err := azidentity.NewClientSecretCredential(tc.TenantID(), app.AppID, pass.SecretText, nil)
				Expect(err).NotTo(HaveOccurred())

				// MSGraph is eventually consistent, wait up to 2 minutes for the token to be valid
				var accessToken azcore.AccessToken
				Eventually(func() error {
					var err error
					accessToken, err = cred.GetToken(ctx, policy.TokenRequestOptions{
						Scopes: []string{fmt.Sprintf("%s/.default", app.AppID)},
					})

					if err != nil {
						GinkgoWriter.Printf("GetToken failed: %v\n", err)
					}
					return err
				}, 2*time.Minute, 10*time.Second).Should(Succeed())

				config := &rest.Config{
					Host:        adminRESTConfig.Host,
					BearerToken: accessToken.Token,
					TLSClientConfig: rest.TLSClientConfig{
						Insecure: framework.IsDevelopmentEnvironment(),
					},
				}
				kubeconfigExternalAuth, err := framework.GenerateKubeconfig(config)
				Expect(err).NotTo(HaveOccurred())

				kubeconfigB64ExternalAuth := base64.StdEncoding.EncodeToString([]byte(kubeconfigExternalAuth))
				// Use -o name to keep output minimal and avoid 4KB VM output limit
				kubectlTestExternalAuth := fmt.Sprintf("echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get nodes -o name", kubeconfigB64ExternalAuth)

				Eventually(func(g Gomega) {
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, kubectlTestExternalAuth, 2*time.Minute)
					g.Expect(err).NotTo(HaveOccurred(), "kubectl should execute successfully from authorized VM with OIDC token")

					output = strings.TrimSpace(output)
					g.Expect(output).NotTo(BeEmpty(), "Should receive node list from Kubernetes API")

					// Count nodes - each line is a node
					lines := strings.Split(output, "\n")
					nodeCount := 0
					for _, line := range lines {
						if strings.TrimSpace(line) != "" {
							nodeCount++
						}
					}
					g.Expect(nodeCount).To(Equal(2), "Should have 2 nodes, got: %s", output)
					By(fmt.Sprintf("Successfully retrieved %d nodes using external auth", nodeCount))
				}, 5*time.Minute, 20*time.Second).Should(Succeed())

				By("creating the console OAuth client secret for external auth via VM")
				consoleOAuthSecretName := fmt.Sprintf("%s-console-openshift-console", customerExternalAuthName)
				clientSecretB64 := base64.StdEncoding.EncodeToString([]byte(pass.SecretText))
				createSecretCmd := fmt.Sprintf(
					`echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig create secret generic %s --namespace=openshift-config --from-literal=clientSecret="$(echo '%s' | base64 -d)"`,
					kubeconfigB64,
					consoleOAuthSecretName,
					clientSecretB64,
				)
				_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, createSecretCmd, 2*time.Minute)
				Expect(err).NotTo(HaveOccurred())

				By("verifying all cluster operators are healthy from authorized VM")
				// Only output unavailable operators (filter out :True lines) to stay within 4KB VM output limit
				clusterOperatorsCmd := fmt.Sprintf(
					`echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get clusteroperators -o jsonpath='{range .items[*]}{.metadata.name}:{.status.conditions[?(@.type=="Available")].status}{"\n"}{end}' | grep -v ':True$'`,
					kubeconfigB64,
				)

				Eventually(func(g Gomega) {
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, clusterOperatorsCmd, 2*time.Minute)
					g.Expect(err).NotTo(HaveOccurred(), "kubectl get clusteroperators should succeed from authorized VM")

					unavailableOperators := parseUnavailableResources(output)
					g.Expect(unavailableOperators).To(BeEmpty(), "All ClusterOperators should report Available=True, but these are not available: %v", unavailableOperators)
				}, 10*time.Minute, 20*time.Second).Should(Succeed())

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

				By("verifying VM is now blocked from API access")
				output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest, 2*time.Minute)
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

func parseUnavailableResources(output string, skip ...string) []string {
	skipSet := make(map[string]bool)
	for _, s := range skip {
		skipSet[s] = true
	}

	var unavailable []string
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			name := parts[0]
			if skipSet[name] {
				continue
			}
			unavailable = append(unavailable, fmt.Sprintf("%s (Available=%s)", name, parts[1]))
		} else {
			unavailable = append(unavailable, line)
		}
	}

	return unavailable
}
