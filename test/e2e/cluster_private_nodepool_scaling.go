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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test creates a cluster with private KAS and validates that NodePool
// scaling operations work correctly in private topology. Worker nodes must
// be able to join the cluster and reach the KAS via the private endpoint.
var _ = Describe("Customer", func() {
	It("should create a cluster with private KAS and successfully scale nodepool replicas",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.Slow,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "priv-np-scale"
				customerNodePoolName = "np-scale"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "priv-np-scale", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private nodepool scaling test")

			By("creating cluster parameters with private API visibility")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.APIVisibility = "Private"
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private nodepool scaling cluster")

			By("deploying test VM in customer VNet for KAS verification")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for private nodepool scaling verification")

			By("creating the HCP cluster with private KAS")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create private HCP cluster %q", customerClusterName)

			By("verifying cluster API visibility is Private via ARM GET")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20251223preview.VisibilityPrivate),
				"cluster %q API visibility should be Private", customerClusterName)

			By("creating the initial node pool with 1 replica")
			initialReplicas := int32(1)
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = initialReplicas

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create initial node pool %q for private cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials and verifying KAS reachability from VM")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private cluster %q", customerClusterName)

			internalIP, err := framework.GetPrivateKASInternalIP(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to find private KAS internal LB IP")
			GinkgoLogr.Info("Found private KAS internal LB", "ip", internalIP)

			// Connect to the internal LB IP to prove network reachability to KAS from
			// inside the VNet. Skip TLS hostname verification because the server URL
			// uses an IP address rather than the hostname the cert was issued for.
			adminRESTConfig.Insecure = true
			adminRESTConfig.Host = fmt.Sprintf("https://%s:443", internalIP)

			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			versionOutput, err := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "version", 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "kubectl version should succeed from VM via private endpoint (output: %s)", versionOutput)
			Expect(versionOutput).To(ContainSubstring("Server Version"),
				"KAS should be reachable from VM via private endpoint (output: %s)", versionOutput)

			By("verifying initial node count and ready status")
			Eventually(func(g Gomega) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes --no-headers", 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get nodes via VM")
				var nodeLines []string
				for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
					if strings.TrimSpace(l) != "" {
						nodeLines = append(nodeLines, l)
					}
				}
				g.Expect(nodeLines).To(HaveLen(int(initialReplicas)), "expected %d nodes, got output: %s", initialReplicas, output)
				for _, line := range nodeLines {
					g.Expect(line).To(ContainSubstring(" Ready "), "node not in Ready state: %s", line)
				}
			}, framework.NodePoolScalingTimeout, 30*time.Second).Should(Succeed(), "all %d initial nodes should be Ready", initialReplicas)
			GinkgoLogr.Info("Initial node pool verified", "replicas", initialReplicas)

			By("scaling up the nodepool from 1 to 2 replicas")
			scaledUpReplicas := int32(2)
			update := hcpsdk20251223preview.NodePoolUpdate{
				Properties: &hcpsdk20251223preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(scaledUpReplicas),
				},
			}
			scaleUpResp, err := framework.UpdateNodePoolAndWait20251223(ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				framework.NodePoolScalingTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to scale up node pool %q from %d to %d replicas",
				customerNodePoolName, initialReplicas, scaledUpReplicas)
			Expect(scaleUpResp.Properties).NotTo(BeNil(), "scale up response Properties was nil")
			Expect(scaleUpResp.Properties.Replicas).NotTo(BeNil(), "scale up response Properties.Replicas was nil")
			Expect(*scaleUpResp.Properties.Replicas).To(Equal(scaledUpReplicas),
				"expected scale up response replicas to equal %d", scaledUpReplicas)

			By("verifying scaled-up node count and ready status")
			Eventually(func(g Gomega) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes --no-headers", 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get nodes via VM")
				var nodeLines []string
				for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
					if strings.TrimSpace(l) != "" {
						nodeLines = append(nodeLines, l)
					}
				}
				g.Expect(nodeLines).To(HaveLen(int(scaledUpReplicas)), "expected %d nodes after scale up, got output: %s", scaledUpReplicas, output)
				for _, line := range nodeLines {
					g.Expect(line).To(ContainSubstring(" Ready "), "node not in Ready state after scale up: %s", line)
				}
			}, framework.NodePoolScalingTimeout, 30*time.Second).Should(Succeed(), "all %d nodes should be Ready after scale up", scaledUpReplicas)
			GinkgoLogr.Info("Scale up verified", "replicas", scaledUpReplicas)

			By("scaling down the nodepool from 2 to 1 replica")
			update = hcpsdk20251223preview.NodePoolUpdate{
				Properties: &hcpsdk20251223preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(initialReplicas),
				},
			}
			// Scale-down for private clusters takes longer than the standard
			// NodePoolScalingTimeout because the v20251223preview ARM LRO for
			// node deletion exceeds 20 minutes in this topology.
			scaleDownResp, err := framework.UpdateNodePoolAndWait20251223(ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				60*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to scale down node pool %q from %d to %d replicas",
				customerNodePoolName, scaledUpReplicas, initialReplicas)
			Expect(scaleDownResp.Properties).NotTo(BeNil(), "scale down response Properties was nil")
			Expect(scaleDownResp.Properties.Replicas).NotTo(BeNil(), "scale down response Properties.Replicas was nil")
			Expect(*scaleDownResp.Properties.Replicas).To(Equal(initialReplicas),
				"expected scale down response replicas to equal %d", initialReplicas)

			By("verifying scaled-down node count via kubectl from inside the VNet")
			Eventually(func(g Gomega) {
				output, runErr := framework.RunKubectlOnVM(ctx, tc, *resourceGroup.Name, vmName, kubeconfigB64, "get nodes --no-headers", 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get nodes via VM")
				var nodeLines []string
				for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
					if strings.TrimSpace(l) != "" {
						nodeLines = append(nodeLines, l)
					}
				}
				g.Expect(nodeLines).To(HaveLen(int(initialReplicas)),
					"expected %d nodes after scale down, got output: %s", initialReplicas, output)
				for _, line := range nodeLines {
					g.Expect(line).To(ContainSubstring(" Ready "), "node not in Ready state after scale down: %s", line)
				}
			}, 30*time.Minute, 30*time.Second).Should(Succeed(),
				"all %d nodes should be Ready after scale down", initialReplicas)
			GinkgoLogr.Info("Private cluster nodepool scaling verified successfully",
				"clusterName", customerClusterName)
		},
	)
})
