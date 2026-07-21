// Copyright 2025 Microsoft Corporation
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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a HCP cluster and use cilium CNI plugin",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "cilium-cl"
				customerNodePoolName = "cilium-np"
				ciliumNamespace      = "kube-system"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cni-cilium", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for cilium CNI test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("setting no cni network configuration")
			clusterParams.Network.NetworkType = "Other"
			clusterParams.Network.PodCIDR = "10.128.0.0/14"
			clusterParams.Network.ServiceCIDR = "172.30.0.0/16"
			clusterParams.Network.MachineCIDR = "10.0.0.0/16"
			clusterParams.Network.HostPrefix = 23

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cilium CNI cluster")

			By("creating HCP cluster without CNI")
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q without CNI", customerClusterName)

			By("getting credentials and verifying cluster is available")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(), "failed to verify HCP cluster %q is available", customerClusterName)

			By("getting kubeconfig content for Helm")
			kubeconfigContent, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig for cluster %q", customerClusterName)

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
				},
				"prometheus": map[string]any{
					"serviceMonitor": map[string]any{
						"enabled": false,
					},
				},
				"hubble": map[string]any{
					"tls": map[string]any{
						"enabled": false,
					},
				},
			}
			err = framework.InstallCiliumChart(ctx, "1.19.2", ciliumValues, kubeconfigContent, ciliumNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to install Cilium chart via Helm")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolErr := tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			// We delay checking the error on purpose to get more details
			// about the issue by running the verifiers.

			By("checking that cilium is running and nodes are in Ready state")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady(), verifiers.VerifyCiliumOperational(ciliumNamespace, "k8s-app=cilium"))
			Expect(errors.Join(err, nodePoolErr)).NotTo(HaveOccurred(), "failed to verify cilium is running and nodes are Ready for cluster %q", customerClusterName)

			By("checking that network works via a simple web app and connectivity checks")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifySimpleWebApp(), verifiers.VerifyCiliumConnectivityChecks("1.19.2"))
			Expect(err).NotTo(HaveOccurred(), "failed to run simple web app and connectivity check app with cilium CNI")
		},
	)
})
