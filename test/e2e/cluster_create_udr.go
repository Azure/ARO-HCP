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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This is an end-to-end smoke test for ARO-23477 (User Defined Routing). It
// proves outboundType=UserDefinedRouting survives the ARM PUT -> RP -> Cluster
// Service round trip, that CS did not provision the LoadBalancer-outbound
// egress rule/IPs, that a node pool can come up under UDR (nodes reach
// Ready), and the feature-level assertion that actually matters: pod egress
// traffic leaves via the customer's NAT Gateway public IP, not any ARO-owned
// address. The LB outbound-rule assertion exists because NAT Gateway outranks
// LB outbound rules on the same subnet — without it, this test would pass
// identically even if CS regressed and resumed creating the LoadBalancer
// egress path. It does not assert CS/HyperShift-internal state (CS DB column,
// HostedCluster/AzureCluster CR fields, CCM cloud.conf, absence of
// InvalidResourceReference NIC events) since this test only has customer-level
// ARM and guest-cluster API access, not management-cluster access.
var _ = Describe("Customer", func() {
	It("should create a cluster with outboundType=UserDefinedRouting using v20260901preview",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "udr"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "udr", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for UDR test")

			By("creating cluster parameters with UserDefinedRouting outbound type")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OutboundType = "UserDefinedRouting"

			By("creating customer resources (infrastructure and managed identities) with a NAT Gateway for UDR egress")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{"attachNatGateway": true},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for UDR cluster")
			Expect(clusterParams.NatGatewayPublicIP).NotTo(BeEmpty(), "NAT Gateway public IP was empty despite attachNatGateway=true")

			By("creating the HCP cluster with UserDefinedRouting via v20260901preview")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with UserDefinedRouting", customerClusterName)

			By("verifying cluster was created with UserDefinedRouting outbound type via ARM GET")
			clientFactory := tc.Get20260901ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify outbound type", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Platform).ToNot(BeNil(), "cluster %q Properties.Platform was nil", customerClusterName)
			Expect(cluster.Properties.Platform.OutboundType).ToNot(BeNil(), "cluster %q Properties.Platform.OutboundType was nil", customerClusterName)
			Expect(*cluster.Properties.Platform.OutboundType).To(Equal(hcpsdk20260901preview.OutboundType("UserDefinedRouting")),
				"cluster %q outbound type should be UserDefinedRouting", customerClusterName)
			GinkgoLogr.Info("Cluster created with UserDefinedRouting outbound type", "clusterName", customerClusterName)

			By("verifying CS did not provision the LoadBalancer-outbound egress rule or egress IPs")
			// This is the assertion that actually discriminates UDR from LoadBalancer:
			// the NAT Gateway attached to the worker subnet outranks LB outbound rules,
			// so the pod-egress-IP check below would pass identically even if CS
			// regressed and resumed creating the egress outbound rule + 9 public IPs.
			lbState, err := framework.GetNodeOutboundLoadBalancerState(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to get node-outbound load balancer state in managed resource group %q", clusterParams.ManagedResourceGroupName)
			Expect(lbState.OutboundRuleCount).To(Equal(0),
				"expected zero outbound rules on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.OutboundRuleCount)
			Expect(lbState.FrontendIPConfigCount).To(BeNumerically("<=", 1),
				"expected at most 1 frontend IP (the CCM-created ingress IP) on the node-outbound load balancer under UserDefinedRouting, found %d", lbState.FrontendIPConfigCount)
			GinkgoLogr.Info("Confirmed no LoadBalancer-outbound egress rule or egress IPs were provisioned", "outboundRuleCount", lbState.OutboundRuleCount, "frontendIPConfigCount", lbState.FrontendIPConfigCount)

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
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for UDR cluster %q", customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for UDR cluster %q", customerClusterName)

			By("verifying node pool nodes are Ready and schedulable under UserDefinedRouting")
			err = verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, 2).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "node pool %q nodes did not reach Ready for UDR cluster %q", customerNodePoolName, customerClusterName)

			By("verifying basic API access and APIServices are available")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health for UDR cluster %q", customerClusterName)

			By("verifying a simple web app can run, proving node-level image pulls and ingress work under UDR")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on UDR cluster %q", customerClusterName)

			By("verifying pod egress traffic leaves via the customer's NAT Gateway public IP")
			err = verifiers.VerifyNATGatewayEgressIP(clusterParams.NatGatewayPublicIP, framework.NATGatewayEgressVerifyTimeout).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "pod egress IP did not match NAT Gateway public IP %q for UDR cluster %q",
				clusterParams.NatGatewayPublicIP, customerClusterName)
		},
	)
})
