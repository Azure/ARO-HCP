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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/wait"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// THROWAWAY VALIDATION TEST — not intended to be merged. This exists to
// answer one open question from ARO-23477 (User Defined Routing):
//
//	Does a fully private cluster (api.visibility: Private, ingress.type:
//	Private) actually tolerate outboundType=UserDefinedRouting with ZERO
//	egress mechanism attached — no NAT Gateway, no route table, nothing?
//
// The 2026-11-09 product decision claims Private/PublicAndPrivate + UDR
// needs "zero customer action" because API/OAuth/Ignition/Konnectivity all
// resolve to the internal LB inside the customer's VNet and never touch the
// UDR route table. cluster_create_udr_private.go already proves UDR works
// when a real NAT Gateway is attached — it has never tested the zero-egress
// case, because it always passes attachNatGateway=true.
//
// Working hypothesis, derived from reading HyperShift/CAPZ source directly
// (not assumed): nodes should still register with KAS (internal-LB path,
// no egress dependency) but the cluster should NOT fully provision, because
// container image pulls (release payload, operator images) have no
// internal-LB equivalent and ACR Private Link (the fix for that) is not
// built yet. This test is deliberately split into two halves to match that:
// hard assertions for the parts we're confident about (control-plane
// reachability), and bounded, non-fatal evidence-gathering for the part
// that's genuinely uncertain (does the node ever reach Ready; where exactly
// does bootstrapping stall).
//
// Differences from cluster_create_udr_private.go:
//   - customer-infra.bicep gets a new zeroEgress=true parameter instead of
//     attachNatGateway=true: defaultOutboundAccess: false on the subnet,
//     with no NAT Gateway and no route table — genuinely zero egress, not
//     just "no NAT Gateway happens to be attached" (which would still leave
//     Azure's implicit default outbound access in place).
//   - No pod egress-IP check, no sample-app/ingress check: both require
//     pulling an external image, which is exactly the thing in question.
//   - The node-Ready check is NOT a hard pass/fail gate here. We don't know
//     which way it goes, so Expect()-ing "Ready" would assume the premise
//     we're testing, and Expect()-ing "NotReady" would do the same in the
//     other direction. Instead: poll for a bounded window, log the final
//     state and rich diagnostics (pods, ClusterOperators, ClusterVersion)
//     regardless of outcome, and let whoever reads this run's logs draw the
//     conclusion from real evidence.
var _ = Describe("Customer", func() {
	It("[THROWAWAY] should create a fully private cluster with outboundType=UserDefinedRouting and zero egress, and report what actually happens",
		labels.RequireNothing,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		labels.StartPriority(70),
		func(ctx context.Context) {
			const (
				customerClusterName  = "udr-priv-zero"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "udr-priv-zero", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for zero-egress private UDR test")

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
			if framework.IsIncompatibleNightlyVersionError(err) {
				skipMsg := fmt.Sprintf("zero-egress private UDR cluster requires OCP >= 4.22, but default version %q does not satisfy it: %v", clusterParams.OpenshiftVersionId, err)
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to select OpenShift version >= 4.22 for zero-egress private UDR test (default version: %q)", clusterParams.OpenshiftVersionId)
			clusterParams.OpenshiftVersionId = openshiftVersionID

			By("creating customer resources (infrastructure and managed identities) with NO egress mechanism attached")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{"zeroEgress": true},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for zero-egress private UDR cluster")
			Expect(clusterParams.NatGatewayPublicIP).To(BeEmpty(), "expected no NAT Gateway public IP under zero-egress testing, got %q", clusterParams.NatGatewayPublicIP)

			By("deploying test VM in customer VNet")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for zero-egress private UDR verification")

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
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify zero-egress private UDR config", customerClusterName)
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
			GinkgoLogr.Info("Cluster created with UserDefinedRouting outbound type, private KAS, and private ingress, zero egress", "clusterName", customerClusterName, "apiURL", apiURL)

			By("verifying CS did not provision the LoadBalancer-outbound egress rule or egress IPs")
			// Pure ARM read, no dependency on egress existing. Should be
			// identical to the NAT-Gateway-attached private test: CS skips
			// this regardless of what (if anything) is attached downstream.
			lbState, err := framework.GetNodeOutboundLoadBalancerState(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node-outbound load balancer state in managed resource group %q", clusterParams.ManagedResourceGroupName)
			Expect(lbState.OutboundRuleCount).To(Equal(0),
				"expected zero outbound rules on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.OutboundRuleCount)
			Expect(lbState.FrontendIPConfigCount).To(BeNumerically("<=", 1),
				"expected at most 1 frontend IP on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.FrontendIPConfigCount)
			GinkgoLogr.Info("Confirmed no LoadBalancer-outbound egress rule or egress IPs were provisioned", "outboundRuleCount", lbState.OutboundRuleCount, "frontendIPConfigCount", lbState.FrontendIPConfigCount)

			By("verifying API hostname DNS resolves to the internal KAS LB private IP")
			// Pure DNS + ARM read, no egress dependency. This is the part of
			// the "Private survives zero egress" claim we're most confident
			// about — it should be unaffected by whether anything is
			// attached downstream of the subnet.
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
			// If ARM-level node pool provisioning itself blocks on node
			// readiness (not just VMSS/VM creation succeeding), this step
			// will time out here at the standard NodePoolCreationTimeout —
			// that failure, with its existing collectNodePoolFailureArtifacts
			// diagnostics, is itself part of the evidence this test exists
			// to gather. Using the standard timeout rather than inventing a
			// shorter one to avoid introducing an untested code path.
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
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for zero-egress private UDR cluster %q (this failure, if it's a timeout, is itself evidence — see collectNodePoolFailureArtifacts diagnostics above)",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for zero-egress private UDR cluster %q", customerClusterName)

			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig from admin REST config")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			By("verifying KAS is reachable from VM inside the VNet via DNS (the core claim under test)")
			// This is the load-bearing assertion. If this fails, the
			// "Private topology needs zero customer action" decision itself
			// is wrong, not just "ACR Private Link is missing" — a much
			// bigger finding than anything below. Kept as a hard, bounded
			// assertion for exactly that reason.
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
				"KAS was never reachable from VM inside the VNet via DNS — under zero egress, this would mean the Private-topology UDR decision itself needs revisiting, not just the ACR Private Link gap")
			GinkgoLogr.Info("KAS is reachable from VM inside VNet via DNS, even with zero egress attached")

			By("verifying KAS is NOT reachable from outside the VNet")
			// Unrelated to egress — this is a privacy property of the test
			// runner's own network path, kept for parity/completeness.
			Consistently(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second, true)
				g.Expect(err).To(HaveOccurred(),
					"private KAS should not be reachable from outside the VNet, but connection to %s succeeded", apiURL)
			}, 2*time.Minute, 15*time.Second).Should(Succeed(),
				"private KAS should consistently be unreachable from outside the VNet")
			GinkgoLogr.Info("Confirmed KAS is not reachable from outside the VNet")

			By("[EVIDENCE, not a pass/fail gate] observing node pool status under zero egress")
			// Deliberately NOT an Expect(). We don't know whether nodes
			// reach Ready (CNI/CSI DaemonSet images might or might not be
			// required before the Ready condition flips) or genuinely
			// completes to Ready and only later generations fail image
			// pulls. Poll for a bounded window, keep the latest observed
			// state, and log it — the log line itself is the answer, not a
			// pass/fail verdict this test author would be guessing at.
			var lastNodeOutput string
			_ = wait.PollUntilContextTimeout(ctx, 30*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes -o wide --no-headers", 2*time.Minute)
				if runErr == nil {
					lastNodeOutput = output
				} else {
					lastNodeOutput = fmt.Sprintf("(kubectl get nodes failed: %v)", runErr)
				}
				return false, nil // always run the full window; we want the latest state, not an early exit
			})
			GinkgoLogr.Info("===== ZERO EGRESS EVIDENCE: node status after 10 minutes =====", "output", lastNodeOutput)

			By("[EVIDENCE, not a pass/fail gate] capturing pod, ClusterOperator, and ClusterVersion diagnostics under zero egress")
			podOutput, podErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get pods -A -o wide --no-headers", 2*time.Minute)
			if podErr != nil {
				podOutput = fmt.Sprintf("(kubectl get pods -A failed: %v)", podErr)
			}
			GinkgoLogr.Info("===== ZERO EGRESS EVIDENCE: pod status across all namespaces =====", "output", podOutput)

			coOutput, coErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get clusteroperators", 2*time.Minute)
			if coErr != nil {
				coOutput = fmt.Sprintf("(kubectl get clusteroperators failed: %v)", coErr)
			}
			GinkgoLogr.Info("===== ZERO EGRESS EVIDENCE: ClusterOperators =====", "output", coOutput)

			cvOutput, cvErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get clusterversion", 2*time.Minute)
			if cvErr != nil {
				cvOutput = fmt.Sprintf("(kubectl get clusterversion failed: %v)", cvErr)
			}
			GinkgoLogr.Info("===== ZERO EGRESS EVIDENCE: ClusterVersion =====", "output", cvOutput)

			GinkgoLogr.Info("===== ZERO EGRESS EVIDENCE GATHERING COMPLETE — see the four blocks above for the actual answer =====")
		},
	)
})
