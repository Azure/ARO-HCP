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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test creates a cluster with private KAS (api.visibility: Private) using
// the v2026-06-30-preview API and verifies that:
//   - The Kubernetes API server is only reachable from within the VNet
//   - The default ingress remains public (independence of KAS and ingress visibility)
var _ = Describe("Customer", func() {
	It("should create a cluster with private KAS and verify API server is only reachable from VNet while ingress remains public",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "private-kas"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-kas", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private KAS test")

			By("creating cluster parameters with private API visibility")
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.APIVisibility = "Private"
			// Private KAS requires OCP >= 4.22 (CS validation rejects lower versions)
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private KAS cluster")

			By("deploying test VM in customer VNet")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for private KAS verification")

			By("creating the HCP cluster with private KAS via v20260630preview")
			err = tc.CreateHCPClusterFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private KAS", customerClusterName)

			By("verifying cluster API visibility is Private and ingress is Public via ARM GET")
			clientFactory := tc.Get20260630ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify private KAS", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)

			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20260630preview.VisibilityPrivate),
				"cluster %q API visibility should be Private", customerClusterName)
			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created with private KAS", "clusterName", customerClusterName, "apiURL", apiURL)

			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260630preview.IngressTypePublic),
				"cluster %q ingress type should be Public (private KAS must not affect ingress)", customerClusterName)

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
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private KAS cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			// Admin credentials are fetched via ARM (not direct KAS), so this
			// works regardless of KAS visibility. The returned kubeconfig
			// contains the public KAS URL, which resolves via public DNS to
			// the shared ingress — not to the private internal LB. We must
			// override the server URL with the internal LB IP so that kubectl
			// from the VM connects to the private KAS endpoint.
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private KAS cluster %q", customerClusterName)

			By("looking up the private KAS internal load balancer IP")
			internalIP, err := framework.GetPrivateKASInternalIP(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to find private KAS internal LB IP in managed resource group %q", clusterParams.ManagedResourceGroupName)
			GinkgoLogr.Info("Found private KAS internal LB", "ip", internalIP, "managedRG", clusterParams.ManagedResourceGroupName)

			// Override the server URL with the internal LB IP
			adminRESTConfig.Host = fmt.Sprintf("https://%s:443", internalIP)

			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig from admin REST config")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			By("verifying KAS is reachable from VM inside the VNet")
			var previousNodeOutput string
			Eventually(func(g Gomega) {
				output, err := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes -o name", 2*time.Minute)
				g.Expect(err).NotTo(HaveOccurred(), "kubectl get nodes should succeed from VM inside the VNet")

				output = strings.TrimSpace(output)
				if output != previousNodeOutput {
					GinkgoLogr.Info("VM kubectl get nodes", "output", output)
					previousNodeOutput = output
				}
				g.Expect(output).NotTo(BeEmpty(), "should receive node list from KAS via VM inside VNet")

				lines := nonEmptyLines(output)
				g.Expect(len(lines)).To(Equal(2), "expected 2 nodes, got: %s", output)
			}, 5*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying KAS is NOT reachable from outside the VNet")
			err = framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second)
			Expect(err).To(HaveOccurred(),
				"private KAS should not be reachable from outside the VNet, but connection to %s succeeded", apiURL)
			GinkgoLogr.Info("Confirmed KAS is not reachable from outside the VNet", "error", err)

			By("deploying a sample web app via VM to verify public ingress connectivity")
			// With private KAS, we must deploy the app from the VM since KAS
			// is not reachable from the test runner. We apply the same serving
			// app manifests used by framework.DeploySampleApp but via kubectl
			// on the VM.
			sampleAppNS := "e2e-private-kas-app"
			_, err = framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
				fmt.Sprintf("create namespace %s", sampleAppNS), 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %q via VM", sampleAppNS)

			sampleAppManifests, err := framework.SampleAppManifests(sampleAppNS)
			Expect(err).NotTo(HaveOccurred(), "failed to generate sample app manifests")
			manifestsB64 := base64.StdEncoding.EncodeToString([]byte(sampleAppManifests))
			applyCmd := fmt.Sprintf(
				"echo '%s' | base64 -d > /tmp/kubeconfig && echo '%s' | base64 -d | kubectl --kubeconfig=/tmp/kubeconfig apply -f -",
				kubeconfigB64, manifestsB64,
			)
			_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, applyCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample app via VM for public ingress verification")

			By("waiting for the sample app deployment to become ready via VM")
			_, err = framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
				fmt.Sprintf("-n %s rollout status deployment/agnhost-server --timeout=5m", sampleAppNS), 6*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "sample app deployment did not become ready")

			By("getting the route host from the cluster via VM")
			var routeHost string
			Eventually(func(g Gomega) {
				output, err := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
					fmt.Sprintf("-n %s get routes.route.openshift.io agnhost -o jsonpath='{.spec.host}'", sampleAppNS), 2*time.Minute)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get route host from VM")
				routeHost = strings.TrimSpace(output)
				g.Expect(routeHost).NotTo(BeEmpty(), "route host should be assigned")
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
			appURL := "https://" + routeHost
			GinkgoLogr.Info("Sample app route assigned", "url", appURL)

			By("verifying ingress is reachable from outside the VNet (public ingress independence)")
			// The default ingress should be public even though KAS is private.
			// TestHTTPSConnectivity skips TLS validation: we're testing
			// connectivity to the public LB, not cert validity.
			var previousIngressOutput string
			Eventually(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, appURL, 10*time.Second)
				result := "unreachable"
				if err == nil {
					result = "reachable"
				}
				if result != previousIngressOutput {
					GinkgoLogr.Info("Public ingress connectivity check from outside VNet", "result", result)
					previousIngressOutput = result
				}
				g.Expect(err).NotTo(HaveOccurred(),
					"public ingress should be reachable from outside the VNet for private KAS cluster, but got error: %v", err)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
			GinkgoLogr.Info("Confirmed public ingress is reachable from outside the VNet despite private KAS")

			By("verifying all cluster operators are healthy from VM")
			// Only output unavailable operators (filter out :True lines) to stay within 4KB VM output limit
			var previousCOOutput string
			Eventually(func(g Gomega) {
				output, err := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
					`get clusteroperators -o jsonpath='{range .items[*]}{.metadata.name}:{.status.conditions[?(@.type=="Available")].status}{"\n"}{end}' | grep -v ':True$'`, 2*time.Minute)
				g.Expect(err).NotTo(HaveOccurred(), "kubectl get clusteroperators should succeed from VM")

				unavailableOperators := parseUnavailableResources(output)
				summary := fmt.Sprintf("%v", unavailableOperators)
				if summary != previousCOOutput {
					GinkgoLogr.Info("Cluster operator status", "unavailable", unavailableOperators)
					previousCOOutput = summary
				}
				g.Expect(unavailableOperators).To(BeEmpty(),
					"all ClusterOperators should report Available=True, but these are not available: %v", unavailableOperators)
			}, 10*time.Minute, 20*time.Second).Should(Succeed())
			GinkgoLogr.Info("All cluster operators are healthy")
		},
	)
})

// nonEmptyLines splits s by newline and returns only non-empty lines.
func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
