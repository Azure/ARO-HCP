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
	"net"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigyaml "sigs.k8s.io/yaml"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	udrPrivateEgressNamespace      = "e2e-udr-egress"
	udrPrivateEgressPodName        = "nat-gateway-egress"
	udrPrivateEgressServiceAccount = "nat-gateway-egress"
	udrPrivateEgressImage          = "mcr.microsoft.com/azurelinux/distroless/debug:3.0"
	// udrPrivateEgressCheckURL is plain HTTP (not HTTPS) deliberately: the
	// minimal busybox build in udrPrivateEgressImage is not guaranteed to be
	// compiled with TLS support. Mirrors verifiers.VerifyNATGatewayEgressIP's
	// choice; duplicated here (not reused) because this cluster's KAS is
	// private and must be driven through the VM relay
	// (RunKubectlOnVM/RunVMCommand) rather than that verifier's direct
	// client-go path, which cannot reach a private API server from the test
	// runner.
	udrPrivateEgressCheckURL = "http://checkip.amazonaws.com"
)

// This test combines UDR (outboundType=UserDefinedRouting) with a fully
// private cluster (api.visibility: Private AND ingress.type: Private). Per
// the 2026-11-09 product decision, Private/PublicAndPrivate + UDR should need
// zero customer action for API/OAuth/Ignition/Konnectivity reachability
// (they all resolve to the internal LB inside the VNet, never touching the
// UDR route table) — this test exercises that combination directly rather
// than assume it holds.
//
// Modeled on cluster_create_udr.go for the UDR-specific assertions
// (OutboundType round trip, NAT Gateway egress) and
// cluster_create_fully_private.go for the private-cluster mechanics: the
// test runner cannot reach a private KAS directly, so every in-cluster
// check is driven through a VM inside the customer VNet via
// RunKubectlOnVM/RunVMCommand instead of a direct admin REST client.
var _ = Describe("Customer", func() {
	It("should create a fully private cluster with outboundType=UserDefinedRouting and verify both private reachability and NAT Gateway egress",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		labels.StartPriority(70),
		func(ctx context.Context) {
			const (
				customerClusterName  = "udr-private"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "udr-private", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private UDR test")

			By("creating cluster parameters with UserDefinedRouting outbound type, private KAS, and private ingress")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OutboundType = "UserDefinedRouting"
			clusterParams.APIVisibility = "Private"
			clusterParams.IngressType = "Private"
			clusterParams.DisableSwift = false

			// Fully-private clusters require OCP >= 4.22 (CS validation rejects lower versions).
			openshiftVersionID, err := framework.PickAtLeastOpenshiftVersionId(clusterParams.OpenshiftVersionId, "4.22")
			// If the default is nightly which isn't >= 4.22, skip this test with an explanation.
			if framework.IsIncompatibleNightlyVersionError(err) {
				skipMsg := fmt.Sprintf("private UDR cluster requires OCP >= 4.22, but default version %q does not satisfy it: %v", clusterParams.OpenshiftVersionId, err)
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to select OpenShift version >= 4.22 for private UDR test (default version: %q)", clusterParams.OpenshiftVersionId)
			clusterParams.OpenshiftVersionId = openshiftVersionID

			By("creating customer resources (infrastructure and managed identities) with a NAT Gateway for UDR egress")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{"attachNatGateway": true},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private UDR cluster")
			Expect(clusterParams.NatGatewayPublicIP).NotTo(BeEmpty(), "NAT Gateway public IP was empty despite attachNatGateway=true")

			By("deploying test VM in customer VNet")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for private UDR verification")

			By("creating the HCP cluster with UserDefinedRouting, private KAS, and private ingress")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create private HCP cluster %q with UserDefinedRouting", customerClusterName)

			By("verifying outbound type, API visibility, and ingress type via ARM GET")
			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify private UDR config", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Platform).ToNot(BeNil(), "cluster %q Properties.Platform was nil", customerClusterName)
			Expect(cluster.Properties.Platform.OutboundType).ToNot(BeNil(), "cluster %q Properties.Platform.OutboundType was nil", customerClusterName)
			Expect(*cluster.Properties.Platform.OutboundType).To(Equal(hcpsdk20260901preview.OutboundType("UserDefinedRouting")),
				"cluster %q outbound type should be UserDefinedRouting", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20260901preview.VisibilityPrivate),
				"cluster %q API visibility should be Private", customerClusterName)
			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260901preview.IngressTypePrivate),
				"cluster %q ingress type should be Private", customerClusterName)
			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created with UserDefinedRouting outbound type, private KAS, and private ingress", "clusterName", customerClusterName, "apiURL", apiURL)

			By("verifying CS did not provision the LoadBalancer-outbound egress rule or egress IPs")
			// Same discriminating assertion as cluster_create_udr.go: the NAT
			// Gateway attached to the worker subnet outranks LB outbound rules,
			// so the pod-egress-IP check below would pass identically even if CS
			// regressed and resumed creating the egress outbound rule + 9 public
			// IPs. GetNodeOutboundLoadBalancerState excludes only "int-"-prefixed
			// LBs (HyperShift's private KAS/ingress LBs); CS never sets
			// OutboundRules on any LB it doesn't own, so this remains a valid
			// signal regardless of how many internal LBs private topology adds.
			lbState, err := framework.GetNodeOutboundLoadBalancerState(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node-outbound load balancer state in managed resource group %q", clusterParams.ManagedResourceGroupName)
			Expect(lbState.OutboundRuleCount).To(Equal(0),
				"expected zero outbound rules on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.OutboundRuleCount)
			Expect(lbState.FrontendIPConfigCount).To(BeNumerically("<=", 1),
				"expected at most 1 frontend IP on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.FrontendIPConfigCount)
			GinkgoLogr.Info("Confirmed no LoadBalancer-outbound egress rule or egress IPs were provisioned", "outboundRuleCount", lbState.OutboundRuleCount, "frontendIPConfigCount", lbState.FrontendIPConfigCount)

			By("verifying API hostname DNS resolves to the internal KAS LB private IP")
			// Fully-private clusters have two internal LBs; GetPrivateKASInternalIP
			// uses the "kube-apiserver" load balancing rule to select the KAS one.
			lbIP, err := framework.GetPrivateKASInternalIP(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to find private KAS internal LB IP in managed resource group %q", clusterParams.ManagedResourceGroupName)
			GinkgoLogr.Info("Found private KAS internal LB IP", "ip", lbIP, "managedRG", clusterParams.ManagedResourceGroupName)

			parsedURL, err := url.Parse(apiURL)
			Expect(err).NotTo(HaveOccurred(), "failed to parse API URL %q", apiURL)
			apiHost := parsedURL.Hostname()
			ips, err := net.DefaultResolver.LookupHost(ctx, apiHost)
			Expect(err).NotTo(HaveOccurred(), "DNS lookup failed for API hostname %q", apiHost)
			Expect(ips).NotTo(BeEmpty(), "DNS lookup returned no IPs for API hostname %q", apiHost)
			GinkgoLogr.Info("API hostname DNS resolved", "hostname", apiHost, "ips", ips)
			Expect(ips).To(ContainElement(lbIP),
				"API hostname %q resolved to %v but expected it to include internal KAS LB IP %s", apiHost, ips, lbIP)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private UDR cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private UDR cluster %q", customerClusterName)

			// The API hostname in the kubeconfig resolves (via public DNS) to the
			// private IP of the KAS internal load balancer, so kubectl on the VM
			// connects through the private endpoint without any host override.
			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig from admin REST config")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			By("verifying KAS is reachable from VM inside the VNet via DNS")
			var lastKubectlErr error
			Eventually(func(g Gomega) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "version", 2*time.Minute)
				if runErr != nil && (lastKubectlErr == nil || runErr.Error() != lastKubectlErr.Error()) {
					GinkgoLogr.Info("KAS not yet reachable from VM via DNS", "error", runErr, "output", output)
					lastKubectlErr = runErr
				}
				g.Expect(runErr).NotTo(HaveOccurred(),
					"kubectl version should succeed from VM via DNS-resolved private KAS (output: %s)", output)
			}, 5*time.Minute, 15*time.Second).Should(Succeed(),
				"KAS was never reachable from VM inside the VNet via DNS")
			GinkgoLogr.Info("KAS is reachable from VM inside VNet via DNS")

			By("verifying node pool nodes are Ready via VM")
			Eventually(func(g Gomega) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes --no-headers", 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get nodes via VM")
				var nodeLines []string
				for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
					if strings.TrimSpace(l) != "" {
						nodeLines = append(nodeLines, l)
					}
				}
				g.Expect(nodeLines).To(HaveLen(2), "expected 2 nodes, got output: %s", output)
				for _, line := range nodeLines {
					g.Expect(line).To(ContainSubstring(" Ready "), "node not in Ready state: %s", line)
				}
			}, framework.NodePoolScalingTimeout, 30*time.Second).Should(Succeed(), "all 2 nodes should be Ready")
			GinkgoLogr.Info("Node pool nodes verified as Ready")

			By("verifying KAS is NOT reachable from outside the VNet")
			Consistently(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second, true)
				g.Expect(err).To(HaveOccurred(),
					"private KAS should not be reachable from outside the VNet, but connection to %s succeeded", apiURL)
			}, 2*time.Minute, 15*time.Second).Should(Succeed(),
				"private KAS should consistently be unreachable from outside the VNet")
			GinkgoLogr.Info("Confirmed KAS is not reachable from outside the VNet")

			By("deploying a sample web app to verify ingress connectivity from within the VNet")
			sampleAppManifests, err := framework.SampleAppManifests("e2e-sample-app")
			Expect(err).NotTo(HaveOccurred(), "failed to generate sample app manifests")
			applyCmd := fmt.Sprintf(
				"KUBECONFIG=$(mktemp) && "+
					"trap 'rm -f $KUBECONFIG' EXIT && "+
					"echo '%s' | base64 -d > $KUBECONFIG && "+
					"chmod 600 $KUBECONFIG && "+
					"kubectl --kubeconfig=$KUBECONFIG create namespace e2e-sample-app --dry-run=client -o yaml 2>/dev/null | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>/dev/null && "+
					"echo '%s' | base64 -d | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>/dev/null",
				kubeconfigB64,
				base64.StdEncoding.EncodeToString([]byte(sampleAppManifests)),
			)
			applyOutput, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, applyCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(),
				"failed to deploy sample app from VM (output: %s)", applyOutput)
			GinkgoLogr.Info("Sample app deployed from VM", "output", applyOutput)

			By("getting the sample app route host via VM")
			var routeHost string
			Eventually(func(g Gomega) {
				routeCmd := fmt.Sprintf(
					"KUBECONFIG=$(mktemp) && "+
						"trap 'rm -f $KUBECONFIG' EXIT && "+
						"echo '%s' | base64 -d > $KUBECONFIG && "+
						"chmod 600 $KUBECONFIG && "+
						"kubectl --kubeconfig=$KUBECONFIG get route -n e2e-sample-app agnhost -o jsonpath='{.spec.host}' 2>/dev/null",
					kubeconfigB64,
				)
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, routeCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get route host from VM")
				routeHost = strings.TrimSpace(output)
				g.Expect(routeHost).NotTo(BeEmpty(), "route host should not be empty")
			}, 15*time.Minute, 15*time.Second).Should(Succeed(), "sample app route should become available")

			appURL := "https://" + routeHost
			GinkgoLogr.Info("Sample app route available", "url", appURL)

			By("verifying ingress is reachable from VM inside the VNet")
			curlCmd := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s", appURL)
			Eventually(func(g Gomega) {
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, curlCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for ingress connectivity test")
				httpCode := strings.TrimSpace(output)
				g.Expect(httpCode).To(Equal("200"),
					"expected HTTP 200 from sample app via private ingress, got %s", httpCode)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
			GinkgoLogr.Info("Confirmed ingress is reachable from VM inside the VNet")

			By("verifying ingress is NOT reachable from outside the VNet")
			Consistently(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, appURL, 10*time.Second, true)
				g.Expect(err).To(HaveOccurred(),
					"private ingress should not be reachable from outside the VNet, but connection succeeded")
			}, 2*time.Minute, 15*time.Second).Should(Succeed(),
				"private ingress should consistently be unreachable from outside the VNet")
			GinkgoLogr.Info("Confirmed ingress is not reachable from outside the VNet")

			By("verifying pod egress traffic leaves via the customer's NAT Gateway public IP")
			// Built as typed corev1 objects (same shape as
			// verifiers.VerifyNATGatewayEgressIP's pod) and marshaled to YAML for
			// `kubectl apply -f -` via the VM, since this cluster's KAS is private
			// and the direct client-go path that verifier uses cannot reach it.
			//
			// A dedicated ServiceAccount is applied explicitly rather than relying
			// on the namespace's auto-provisioned "default" one: that account is
			// created asynchronously by the service-account controller, so a pod
			// referencing it immediately after namespace creation can race it and
			// fail admission with "serviceaccount \"default\" not found". Applying
			// our own via kubectl apply (which waits for the write to complete)
			// before applying the pod avoids that race.
			egressServiceAccount := &corev1.ServiceAccount{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      udrPrivateEgressServiceAccount,
					Namespace: udrPrivateEgressNamespace,
				},
			}
			egressServiceAccountYAML, err := sigyaml.Marshal(egressServiceAccount)
			Expect(err).NotTo(HaveOccurred(), "failed to marshal NAT Gateway egress check service account")

			automountSAToken := false
			egressPod := &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      udrPrivateEgressPodName,
					Namespace: udrPrivateEgressNamespace,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					ServiceAccountName:           udrPrivateEgressServiceAccount,
					AutomountServiceAccountToken: &automountSAToken,
					Containers: []corev1.Container{{
						Name:    "egress-check",
						Image:   udrPrivateEgressImage,
						Command: []string{"busybox", "wget", "-qO-", udrPrivateEgressCheckURL},
					}},
				},
			}
			egressPodYAML, err := sigyaml.Marshal(egressPod)
			Expect(err).NotTo(HaveOccurred(), "failed to marshal NAT Gateway egress check pod")

			deployEgressPodCmd := fmt.Sprintf(
				"KUBECONFIG=$(mktemp) && "+
					"trap 'rm -f $KUBECONFIG' EXIT && "+
					"echo '%s' | base64 -d > $KUBECONFIG && "+
					"chmod 600 $KUBECONFIG && "+
					"kubectl --kubeconfig=$KUBECONFIG create namespace %s --dry-run=client -o yaml 2>/dev/null | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>/dev/null && "+
					"echo '%s' | base64 -d | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>/dev/null && "+
					"echo '%s' | base64 -d | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>/dev/null",
				kubeconfigB64,
				udrPrivateEgressNamespace,
				base64.StdEncoding.EncodeToString(egressServiceAccountYAML),
				base64.StdEncoding.EncodeToString(egressPodYAML),
			)
			deployOutput, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, deployEgressPodCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy NAT Gateway egress check pod from VM (output: %s)", deployOutput)

			Eventually(func(g Gomega) {
				phase, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
					fmt.Sprintf("get pod -n %s %s -o jsonpath='{.status.phase}'", udrPrivateEgressNamespace, udrPrivateEgressPodName), 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get NAT Gateway egress check pod status via VM")
				g.Expect(strings.TrimSpace(phase)).To(Equal("Succeeded"),
					"NAT Gateway egress check pod phase is %q, expected Succeeded", strings.TrimSpace(phase))
			}, framework.NATGatewayEgressVerifyTimeout, 15*time.Second).Should(Succeed(),
				"NAT Gateway egress check pod never reached Succeeded")

			observedIPOutput, err := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64,
				fmt.Sprintf("logs -n %s %s", udrPrivateEgressNamespace, udrPrivateEgressPodName), 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to get NAT Gateway egress check pod logs via VM")
			observedIP := strings.TrimSpace(observedIPOutput)
			Expect(observedIP).To(Equal(clusterParams.NatGatewayPublicIP),
				"pod egress source IP %q does not match NAT Gateway public IP %q for private UDR cluster %q",
				observedIP, clusterParams.NatGatewayPublicIP, customerClusterName)
			GinkgoLogr.Info("Confirmed pod egress traffic leaves via the NAT Gateway public IP", "ip", observedIP)
		},
	)
})
