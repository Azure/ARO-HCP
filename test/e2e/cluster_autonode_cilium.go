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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test checks Cilium and AutoNode on the same cluster. It covers two
// questions:
//   - Does Karpenter provision nodes correctly when Cilium is the CNI?
//   - Does pod traffic work between a Karpenter node and a regular worker, and
//     between two Karpenter nodes?
//
// Two workload replicas with a required anti-affinity rule make Karpenter create
// two nodes. The test then runs two pod-IP checks: Karpenter to worker and
// Karpenter to Karpenter.
//
// Cilium startup taint: the NodePool declares node.cilium.io/agent-not-ready as
// a startup taint. The test also asserts that each NodeClaim declares it. See
// https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
//
// Deliberate limits: the Cilium connectivity suite, DNS resolution, and the DNS
// CiliumNetworkPolicy belong to the dedicated Cilium spec. This spec sends pod
// traffic by IP, so it does not exercise DNS or Service routing. It does check
// node placement, the per-node IPAM allocation, and the tunnel settings. It does
// not capture VXLAN packets.
var _ = Describe("Customer", func() {
	It("should be able to create a cluster with autonode enabled and cilium CNI on OCP version >= 4.22 and run cross-node pod traffic from the Karpenter-provisioned node",
		labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const clusterName = "autonode-cilium-422"
			const ciliumNamespace = "kube-system"
			// One budget covers node initialization and Cilium DaemonSet
			// readiness.
			const karpenterNodeReadinessTimeout = 10 * time.Minute
			tc := framework.NewTestContext()

			By("checking that the v20270201 API is deployed before creating Azure resources")
			apiDeployed, probeErr := framework.IsAPI20270201Deployed(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient())
			Expect(probeErr).NotTo(HaveOccurred(), "failed to probe the v20270201 API")
			if !apiDeployed {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autonode-cilium", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for AutoNode with Cilium")

			By("creating cluster parameters with AutoNode enabled and no default CNI")
			clusterParams := framework.NewDefaultClusterParams20270201()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = autoNodeTestOpenshiftVersion
			clusterParams.AutoNodeMode = to.Ptr(hcpsdk20270201.AutoNodeModeEnabled)
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.Network.NetworkType = "Other"
			clusterParams.Network.PodCIDR = "10.128.0.0/14"
			clusterParams.Network.ServiceCIDR = "172.30.0.0/16"
			clusterParams.Network.MachineCIDR = "10.0.0.0/16"
			clusterParams.Network.HostPrefix = 23

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20270201(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for AutoNode with Cilium")

			By("verifying the autonode data-plane operator identity was provisioned")
			validateAutoNodeIdentity20270201(clusterParams.UserAssignedIdentitiesProfile)

			By("creating the HCP cluster with AutoNode enabled and no default CNI")
			err = tc.CreateHCPClusterFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with AutoNode enabled and no default CNI")

			By("verifying the typed AutoNode field round-trips on the created cluster")
			validateAutoNodeEnabled20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, tc.SubscriptionID)

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", clusterName)
			adminRESTConfig.QPS = 50
			adminRESTConfig.Burst = 100

			By("verifying the cluster is available with AutoNode enabled")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %s is available", clusterName)

			By("getting kubeconfig content for Helm")
			kubeconfigContent, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig for cluster %q", clusterName)

			By("providing a Multus-compatible custom CNI conflist for Cilium")
			err = framework.EnsureCiliumCNIConfigMap(ctx, adminRESTConfig, ciliumNamespace, framework.CiliumCNIConfigMapName, framework.CiliumConflistPortmap)
			Expect(err).NotTo(HaveOccurred(), "failed to create Cilium CNI conflist ConfigMap")

			By("installing Cilium via Helm")
			ciliumValues := map[string]any{
				"debug": map[string]any{
					"enabled": true,
				},
				"k8s": map[string]any{
					"requireIPv4PodCIDR": true,
				},
				"logSystemLoad": true,
				"bpf": map[string]any{
					"preallocateMaps": true,
				},
				"ipv4": map[string]any{
					"enabled": true,
				},
				"ipv6": map[string]any{
					"enabled": false,
				},
				"identityChangeGracePeriod": "0s",
				"ipam": map[string]any{
					"mode": "cluster-pool",
					"operator": map[string]any{
						"clusterPoolIPv4PodCIDRList": clusterParams.Network.PodCIDR,
						"clusterPoolIPv4MaskSize":    clusterParams.Network.HostPrefix,
					},
				},
				"endpointRoutes": map[string]any{
					"enabled": true,
				},
				"tunnelPort": 4789,
				"cni": map[string]any{
					"binPath":      "/var/lib/cni/bin",
					"confPath":     "/var/run/multus/cni/net.d",
					"chainingMode": "portmap",
					"configMap":    framework.CiliumCNIConfigMapName,
				},
				"prometheus": map[string]any{
					"serviceMonitor": map[string]any{
						"enabled": false,
					},
				},
				// The chart sets operator.replicas to 2 with a required hostname
				// anti-affinity. With one regular worker, the second replica stays
				// Pending for the whole run. One replica is enough here and keeps
				// the cluster free of pending pods.
				"operator": map[string]any{
					"replicas": 1,
				},
				"hubble": map[string]any{
					"tls": map[string]any{
						"enabled": false,
					},
				},
			}
			err = framework.InstallCiliumChart(ctx, "1.19.2", ciliumValues, kubeconfigContent, ciliumNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to install Cilium chart via Helm")

			By("creating one regular worker for cluster services and for the non-Karpenter traffic target")
			// The Cilium connectivity suite needs two nodes, because its artifacts
			// use required anti-affinity rules. That suite runs in the dedicated
			// Cilium spec. This spec needs one regular worker. Cluster services run
			// there, and the cross-node check uses it as the non-Karpenter target.
			workerVersion := resolveAutoNodeWorkerVersion(ctx, adminRESTConfig, clusterName)
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = "workers"
			nodePoolParams.OpenshiftVersionId = workerVersion
			nodePoolParams.Replicas = 1
			workerErr := tc.CreateNodePoolFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, clusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			// Keep this error. The readiness check below adds the cluster state.
			// Join both errors, so a failed pool is not hidden.

			By("allowing DNS pods to reach the kube-apiserver-proxy via CiliumNetworkPolicy")
			policyErr := framework.EnsureDNSAllowHostAPIServerCiliumNetworkPolicy(ctx, adminRESTConfig, framework.NodePoolCreationTimeout)

			By("checking that Cilium is running and regular workers are Ready")
			readinessErr := verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady(), verifiers.VerifyCiliumOperational(ciliumNamespace, "k8s-app=cilium"))
			Expect(errors.Join(workerErr, policyErr, readinessErr)).NotTo(HaveOccurred(), "failed to create regular workers, allow DNS access, and verify Cilium readiness for cluster %q", clusterName)

			// The cilium-operator writes node.cilium.io/agent-not-ready with effect
			// NoSchedule and an empty value. Karpenter matches startup taints by key
			// and effect, so the list declares both effects. A missing entry makes
			// Karpenter treat the new node as unschedulable and provision more nodes.
			startupTaints := []corev1.Taint{
				{Key: "node.cilium.io/agent-not-ready", Value: "true", Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoSchedule},
			}

			// Two replicas with a required anti-affinity rule. Karpenter plans both
			// NodeClaims in one scheduling pass and launches them in parallel. The
			// test then has two Karpenter nodes: one path to the regular worker, one
			// path between the Karpenter nodes.
			const karpenterNodes = 2
			// Bound: two desired nodes plus two allowed replacements. A larger count
			// points to a startup taint that Karpenter does not know about.
			const maxConcurrentNodeClaims = karpenterNodes + 2

			run := provisionKarpenterNode(ctx, adminRESTConfig, clusterName, karpenterRunOptions{
				replicas:      karpenterNodes,
				startupTaints: startupTaints,
				provisioningChecks: verifiers.KarpenterProvisioningChecks{
					ExpectedStartupTaints:   startupTaints,
					MaxConcurrentNodeClaims: maxConcurrentNodeClaims,
				},
			})

			By("verifying that every Karpenter node is initialized, untainted, and served by a Ready Cilium agent")
			// Check each node by name. A healthy first node must not hide a second
			// node that did not leave the startup phase.
			nodeVerifiers := make([]verifiers.HostedClusterVerifier, 0, len(run.nodeNames)+1)
			for _, nodeName := range run.nodeNames {
				nodeVerifiers = append(nodeVerifiers, verifiers.VerifyKarpenterNodeProvisioned(run.resourceName, karpenterNodeReadinessTimeout, verifiers.KarpenterProvisioningChecks{
					NodeName:                nodeName,
					RequireInitialized:      true,
					AbsentTaintKeys:         []string{"node.cilium.io/agent-not-ready", "karpenter.sh/unregistered"},
					MaxConcurrentNodeClaims: maxConcurrentNodeClaims,
				}))
			}
			nodeVerifiers = append(nodeVerifiers, verifiers.VerifyDaemonSetReady(ciliumNamespace, "cilium", karpenterNodeReadinessTimeout))
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, nodeVerifiers...)
			Expect(err).NotTo(HaveOccurred(), "failed to verify that each Karpenter node is initialized and served by Cilium")

			// Both paths use a pod IP. DNS and Service routing are not part of these
			// checks. The verifier asserts node placement, the per-node Cilium
			// allocation, and the tunnel settings from the Cilium ConfigMap.
			By("verifying direct pod-IP traffic from a Karpenter node to the regular worker")
			err = verifiers.VerifyCiliumCrossNodePodTraffic(verifiers.CiliumCrossNodeTrafficOptions{
				CiliumNamespace: ciliumNamespace, SourceNodeName: run.nodeNames[0],
				ExpectedPodCIDR: "10.128.0.0/14", ExpectedHostPrefix: 23, ExpectedTunnelPort: 4789,
			}, 5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to send pod-IP traffic from a Karpenter node to the regular worker")

			By("verifying direct pod-IP traffic between the two Karpenter nodes")
			err = verifiers.VerifyCiliumCrossNodePodTraffic(verifiers.CiliumCrossNodeTrafficOptions{
				CiliumNamespace: ciliumNamespace,
				SourceNodeName:  run.nodeNames[0],
				TargetNodeName:  run.nodeNames[1],
				ExpectedPodCIDR: "10.128.0.0/14", ExpectedHostPrefix: 23, ExpectedTunnelPort: 4789,
			}, 5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to send pod-IP traffic between the two Karpenter nodes")

			deprovisionKarpenterNode(ctx, adminRESTConfig, run)
		})
})
