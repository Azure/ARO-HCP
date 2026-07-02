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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should create a cluster with private ingress and verify the ingress is internal",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "private-ingress"
				customerNodePoolName = "np-1"
				apiVersion           = "2026-06-30-preview"
			)

			tc := framework.NewTestContext()

			By("checking API version availability")
			if !framework.IsDevelopmentEnvironment() {
				resourcesFactory, err := tc.GetARMResourcesClientFactory(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to get ARM resources client factory")

				providersClient := resourcesFactory.NewProvidersClient()
				provider, err := providersClient.Get(ctx, "Microsoft.RedHatOpenShift", nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft.RedHatOpenShift resource provider")

				available := false
				for _, rt := range provider.ResourceTypes {
					if rt.ResourceType == nil || !strings.EqualFold(*rt.ResourceType, "hcpOpenShiftClusters") {
						continue
					}
					for _, v := range rt.APIVersions {
						if v != nil && strings.EqualFold(*v, apiVersion) {
							available = true
							break
						}
					}
				}
				if !available {
					Skip(fmt.Sprintf("API version %s is not available for Microsoft.RedHatOpenShift/hcpOpenShiftClusters in this environment", apiVersion))
				}
				GinkgoLogr.Info("API version available", "version", apiVersion)
			}

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

			By("deploying test VM in the same VNet for connectivity verification")
			sshPublicKey, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate SSH key pair for test VM")

			vmName := fmt.Sprintf("%s-test-vm", customerClusterName)
			vmSize, err := tc.SelectVMSize(ctx, framework.JumpboxVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a jumpbox VM size for private ingress test")

			var deployErr error
			for attempt := 0; attempt < 3; attempt++ {
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
				time.Sleep(20 * time.Second)
			}
			Expect(deployErr).NotTo(HaveOccurred(), "failed to deploy test VM for private ingress verification")

			By("creating the HCP cluster with private ingress via v20260630preview")
			err = tc.CreateHCPClusterFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
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

			By("verifying IngressController has internal load balancer scope")
			opClient, err := operatorclient.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create operator client from admin REST config")

			var ic *operatorv1.IngressController
			Eventually(func(g Gomega) {
				var getErr error
				ic, getErr = opClient.OperatorV1().IngressControllers("openshift-ingress-operator").Get(ctx, "default", metav1.GetOptions{})
				g.Expect(getErr).NotTo(HaveOccurred(), "failed to get default IngressController")
				g.Expect(ic.Spec.EndpointPublishingStrategy).ToNot(BeNil(), "IngressController endpointPublishingStrategy was nil")
				g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer).ToNot(BeNil(), "IngressController loadBalancer config was nil")
				g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope).To(
					Equal(operatorv1.InternalLoadBalancer),
					"IngressController loadBalancer scope should be Internal for private ingress, got %q",
					ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope)
			}, 5*time.Minute, 15*time.Second).Should(Succeed())

			GinkgoLogr.Info("IngressController verified with internal load balancer scope",
				"scope", ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope)

			By("getting ingress URL from console Route")
			rtClient, err := routeclient.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create route client from admin REST config")

			var consoleURL string
			Eventually(func(g Gomega) {
				route, getErr := rtClient.RouteV1().Routes("openshift-console").Get(ctx, "console", metav1.GetOptions{})
				g.Expect(getErr).NotTo(HaveOccurred(), "failed to get console Route")
				g.Expect(route.Spec.Host).NotTo(BeEmpty(), "console Route host should not be empty")
				consoleURL = "https://" + route.Spec.Host
			}, 5*time.Minute, 15*time.Second).Should(Succeed())

			GinkgoLogr.Info("Console URL resolved from Route", "url", consoleURL)

			By("verifying ingress is reachable from VM inside the VNet")
			curlCmd := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s", consoleURL)
			var previousOutput string
			Eventually(func(g Gomega) {
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, curlCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for ingress connectivity test")

				httpCode := strings.TrimSpace(output)
				if httpCode != previousOutput {
					GinkgoLogr.Info("VM ingress connectivity check", "httpCode", httpCode)
					previousOutput = httpCode
				}
				g.Expect(httpCode).NotTo(Equal("000"),
					"should receive HTTP response from internal LB via VM, got curl error code 000")
			}, 5*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying ingress is NOT reachable from outside the VNet")
			err = testIngressConnectivity(consoleURL, 10*time.Second)
			Expect(err).To(HaveOccurred(),
				"private ingress should not be reachable from outside the VNet, but connection succeeded")
			GinkgoLogr.Info("Confirmed ingress is not reachable from outside the VNet")
		},
	)
})

// testIngressConnectivity attempts an HTTPS connection to the given URL.
// Returns nil if the connection succeeds, or an error if it fails.
func testIngressConnectivity(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
