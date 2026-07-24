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
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	operatorv1 "github.com/openshift/api/operator/v1"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test creates a cluster with private ingress using the v2026-06-30-preview
// API and verifies the ingress is internal. It also serves as the basic
// v2026-06-30-preview API version smoke test — verifying cluster creation,
// credentials, and cluster health — to avoid creating multiple clusters in CI.
var _ = Describe("Customer", func() {
	It("should create a cluster with private ingress using v20260630preview and verify the ingress is internal",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "private-ingress"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-ingress", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private ingress test")

			By("creating cluster parameters with private ingress")
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.IngressType = "Private"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private ingress cluster")

			By("creating the HCP cluster with private ingress via v20260630preview")
			err = tc.CreateHCPClusterFromParam20260630(ctx,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private ingress", customerClusterName)

			By("verifying cluster was created with private ingress type via ARM GET")
			clientFactory := tc.Get20260630ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify private ingress", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260630preview.IngressTypePrivate),
				"cluster %q ingress type should be Private", customerClusterName)
			GinkgoLogr.Info("Cluster created with private ingress", "clusterName", customerClusterName)

			By("deploying test VM in the same VNet for connectivity verification")
			sshPublicKey, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate SSH key pair for test VM")

			vmName := fmt.Sprintf("%s-test-vm", customerClusterName)
			vmSize, err := tc.SelectVMSize(ctx, framework.JumpboxVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a jumpbox VM size for private ingress test")

			var deployErr error
			for attempt := 0; attempt < 3; attempt++ {
				if attempt > 0 {
					time.Sleep(20 * time.Second)
				}
				_, deployErr = tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/test-vm.json"),
					framework.WithDeploymentName("test-vm"),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"vmName":       vmName,
						"vnetName":     clusterParams.VnetName,
						"subnetName":   clusterParams.SubnetName,
						"sshPublicKey": sshPublicKey,
						"vmSize":       vmSize,
					}),
					framework.WithTimeout(30*time.Minute),
				)
				if deployErr == nil || strings.Contains(deployErr.Error(), "SkuNotAvailable") {
					break
				}
			}
			Expect(deployErr).NotTo(HaveOccurred(), "failed to deploy test VM for private ingress verification")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260630()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private ingress cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private ingress cluster %q", customerClusterName)

			By("ensuring the cluster is viable (basic v2026 API verification)")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyIngressControllerScope(operatorv1.InternalLoadBalancer),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health or IngressController scope for cluster %q", customerClusterName)
			GinkgoLogr.Info("Cluster health and IngressController scope verified")

			By("deploying a sample web app to verify ingress connectivity")
			sampleApp, err := framework.DeploySampleApp(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample web app for ingress connectivity test")

			appURL := "https://" + sampleApp.RouteHost
			GinkgoLogr.Info("Sample app deployed", "url", appURL)

			By("verifying ingress is reachable from VM inside the VNet")
			// -k skips TLS validation: we're testing connectivity to the internal LB,
			// not cert validity (which is covered by VerifySimpleWebApp).
			curlCmd := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s", appURL)
			var previousOutput string
			Eventually(func(g Gomega) {
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, curlCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for ingress connectivity test")

				httpCode := strings.TrimSpace(output)
				if httpCode != previousOutput {
					GinkgoLogr.Info("VM ingress connectivity check", "httpCode", httpCode)
					previousOutput = httpCode
				}
				g.Expect(httpCode).To(Equal("200"),
					"expected HTTP 200 from sample app via internal LB, got %s", httpCode)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying ingress is NOT reachable from outside the VNet")
			err = testIngressConnectivity(ctx, appURL, 10*time.Second)
			Expect(err).To(HaveOccurred(),
				"private ingress should not be reachable from outside the VNet, but connection succeeded")
			GinkgoLogr.Info("Confirmed ingress is not reachable from outside the VNet")
		},
	)
})

// testIngressConnectivity attempts an HTTPS connection to the given URL.
// Returns nil if the connection succeeds, or an error if it fails.
// Redirects are disabled to avoid false positives from OAuth redirects.
func testIngressConnectivity(ctx context.Context, url string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
