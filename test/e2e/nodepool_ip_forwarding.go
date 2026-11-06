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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Nodepool IP Forwarding", func() {
	timeBombDeadline := framework.V20270201DeploymentDeadline

	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should create a nodepool with ipForwarding=AllowAllAddresses, have the Azure NIC configured accordingly, and reject a PATCH that changes it",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "ipfwd-cluster"
				customerNodePoolName = "ipfwd-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ipfwd", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for ipForwarding test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("creating the node pool with ipForwarding=AllowAllAddresses via v20270201")
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(1)
			nodePoolParams.IPForwarding = hcpsdk20270201.IPForwardingAllowAllAddresses

			err = tc.CreateNodePoolFromParam20270201(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q with ipForwarding=AllowAllAddresses", customerNodePoolName)

			By("verifying the node pool ARM resource reports ipForwarding=AllowAllAddresses")
			created, err := framework.GetNodePool20270201(ctx,
				tc.Get20270201ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get node pool %q for cluster %q", customerNodePoolName, customerClusterName)
			Expect(created.Properties).ToNot(BeNil(), "nodepool response Properties was nil")
			Expect(created.Properties.Platform).ToNot(BeNil(), "nodepool response Properties.Platform was nil")
			Expect(created.Properties.Platform.IPForwarding).ToNot(BeNil(), "nodepool response Properties.Platform.IPForwarding was nil")
			Expect(*created.Properties.Platform.IPForwarding).To(Equal(hcpsdk20270201.IPForwardingAllowAllAddresses),
				"expected nodepool %q ipForwarding to be AllowAllAddresses", customerNodePoolName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", customerClusterName)

			By("waiting for the node pool's node to be ready and schedulable")
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(
				Succeed(), "expected %d ready and schedulable node(s) in node pool %q", nodePoolParams.Replicas, customerNodePoolName,
			)

			By("resolving the node pool's node(s) to their backing Azure VM network interfaces")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client from admin REST config")

			nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list nodes in cluster %q", customerClusterName)

			poolNodes, err := framework.SelectNodesBelongingToNodePool(nodeList.Items, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to select nodes for node pool %q", customerNodePoolName)
			Expect(poolNodes).NotTo(BeEmpty(), "expected at least one node for node pool %q", customerNodePoolName)

			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			networkFactory, err := tc.GetARMNetworkClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get ARM network client factory")

			By("verifying each node's primary Azure NIC has enableIPForwarding=true")
			for i := range poolNodes {
				node := &poolNodes[i]
				nic, err := framework.ResolveNodePrimaryNIC(ctx, computeFactory, networkFactory, node)
				Expect(err).NotTo(HaveOccurred(), "failed to resolve primary network interface for node %s", node.Name)
				Expect(nic.Properties).ToNot(BeNil(), "network interface for node %s has no properties", node.Name)
				Expect(nic.Properties.EnableIPForwarding).ToNot(BeNil(), "network interface for node %s has no EnableIPForwarding value", node.Name)
				Expect(*nic.Properties.EnableIPForwarding).To(BeTrue(), "expected network interface for node %s to have IP forwarding enabled", node.Name)
			}

			nodePoolsClient := tc.Get20270201ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By("attempting to PATCH the node pool's ipForwarding from AllowAllAddresses to InterfaceAddressesOnly - should be rejected as immutable")
			immutablePatch := hcpsdk20270201.NodePool{
				Properties: &hcpsdk20270201.NodePoolProperties{
					Platform: &hcpsdk20270201.NodePoolPlatformProfile{
						IPForwarding: to.Ptr(hcpsdk20270201.IPForwardingInterfaceAddressesOnly),
					},
				},
			}
			poller, err := nodePoolsClient.BeginUpdate(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, immutablePatch, nil)
			if err == nil {
				_, err = poller.PollUntilDone(ctx, nil)
			}
			Expect(err).To(HaveOccurred(), "expected PATCH changing ipForwarding to be rejected")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"), "error should indicate ipForwarding is immutable")

			By("verifying the node pool's ipForwarding remains AllowAllAddresses after the rejected PATCH")
			afterRejectedPatch, err := framework.GetNodePool20270201(ctx, nodePoolsClient, *resourceGroup.Name, customerClusterName, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node pool %q after rejected PATCH", customerNodePoolName)
			Expect(afterRejectedPatch.Properties).ToNot(BeNil(), "nodepool response Properties was nil")
			Expect(afterRejectedPatch.Properties.Platform).ToNot(BeNil(), "nodepool response Properties.Platform was nil")
			Expect(afterRejectedPatch.Properties.Platform.IPForwarding).ToNot(BeNil(), "nodepool response Properties.Platform.IPForwarding was nil")
			Expect(*afterRejectedPatch.Properties.Platform.IPForwarding).To(Equal(hcpsdk20270201.IPForwardingAllowAllAddresses),
				"expected nodepool %q ipForwarding to remain AllowAllAddresses after rejected PATCH", customerNodePoolName)
		})
})
