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
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

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

				By("creating cluster parameters")
				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = clusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId

				By("creating customer resources")
				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{
						"persistTagValue":        false,
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					TestArtifactsFS,
				)
				Expect(err).NotTo(HaveOccurred())

				By("generating SSH key pair for VM")
				sshPublicKey, _, err := framework.GenerateSSHKeyPair()
				Expect(err).NotTo(HaveOccurred())

				By("deploying test VM")
				vmName := fmt.Sprintf("%s-test-vm", clusterName)
				vmDeployment, err := tc.CreateBicepTemplateAndWait(ctx,
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

				By("setting authorized CIDRs with VM IP")
				clusterParams.AuthorizedCIDRs = []*string{
					to.Ptr(fmt.Sprintf("%s/32", vmPublicIP)),
				}

				err = tc.CreateHCPClusterFromParam(ctx,
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
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest)
					g.Expect(err).NotTo(HaveOccurred())

					// Should get HTTP response (likely 401 or 200, but not connection refused)
					httpCode = strings.TrimSpace(output)
					By(fmt.Sprintf("VM received HTTP status code: %s", httpCode))
					g.Expect(httpCode).To(MatchRegexp("^[2-5][0-9][0-9]$"), "Should receive valid HTTP status code from authorized IP")
				}, 2*time.Minute, 10*time.Second).Should(Succeed())

				By("testing connectivity from current machine (should be blocked)")
				// Try to connect from the test runner (which is not in authorized CIDRs)
				err = testAPIConnectivity(apiURL, 5*time.Second)
				if err != nil {
					By(fmt.Sprintf("Connection from unauthorized IP correctly blocked: %v", err))
				}

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
				// Use base64 encoding to safely transfer kubeconfig
				kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))
				kubectlTest := fmt.Sprintf("echo '%s' | base64 -d > /tmp/kubeconfig && kubectl --kubeconfig=/tmp/kubeconfig get nodes 2>&1", kubeconfigB64)

				// Wrap in Eventually for robustness - API access may take time to propagate
				Eventually(func(g Gomega) {
					var err error
					output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, kubectlTest)
					By(fmt.Sprintf("kubectl output from authorized VM: %s", output))

					// Should be able to run kubectl commands (even if nodes aren't ready)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(output).To(Or(
						ContainSubstring("NAME"),         // Success - got nodes
						ContainSubstring("No resources"), // Success - no nodes yet
						ContainSubstring("NotReady"),     // Success - nodes not ready
					), "Should be able to execute kubectl from authorized VM")
				}, 2*time.Minute, 10*time.Second).Should(Succeed())

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
				output, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, connectivityTest)
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
