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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const customerNSGValidationDelegationServiceName = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"

// Proves customer NSG Deny rules on worker/Swift bootstrap paths affect node pool provisioning:
//
//	1a. outbound Deny Any→worker on 443/6443 — stuck Provisioning
//	1b. outbound Deny Any→worker on all ports except 443/6443 — Succeeded
//	2a. inbound Deny worker→Swift on 8443 — stuck Provisioning
//	2b. inbound Deny worker→Swift on all ports except 8443 — Succeeded
//
// After Deny rules are removed, a new node pool create should still enter Provisioning (recovery control).
var _ = Describe("Customer", func() {
	It("should block or allow node pool provisioning based on which NSG Deny ports are blocked on worker and Swift paths",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.Slow,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "cluster-nsg-val"
				outboundDenyRuleName             = "deny-any-to-worker"
				outboundDenyExceptKASRuleName    = "deny-except-kas"
				inboundDenyRuleName              = "deny-worker-to-swift"
				inboundDenyExcept8443RuleName    = "deny-except-8443"
				aroHCPDelegationName             = "aro-hcp-delegation"
				outboundNodePoolName             = "np-deny-worker"
				outboundExceptKASNodePoolName    = "np-kas-ok"
				inboundNodePoolName              = "np-deny-swift"
				inboundExcept8443NodePoolName    = "np-swift-ok"
				recoveryNodePoolName             = "np-after-deny"

				stuckProvisioningDuration = 15 * time.Minute
				stuckProvisioningInterval = 30 * time.Second
				nodePoolSuccessTimeout    = framework.NodePoolCreationTimeout
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-nsg-val-np", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for customer NSG deny stuck-provisioning test")

			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (worker subnet + vnet-integration subnet)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for Swift NSG deny cluster")
			Expect(clusterParams.SubnetResourceID).NotTo(BeEmpty(), "worker subnet ID was empty after customer infra deploy")
			Expect(clusterParams.VnetIntegrationSubnetID).NotTo(BeEmpty(), "vnet-integration (Swift) subnet ID was empty after customer infra deploy")
			Expect(clusterParams.NsgResourceID).NotTo(BeEmpty(), "cluster NSG resource ID was empty after customer infra deploy")
			Expect(clusterParams.NsgName).NotTo(BeEmpty(), "cluster NSG name was empty after customer infra deploy")

			// Deployed RP may not yet admit max-deletion-duration; drop it so create
			// is not rejected as an unrecognized experimental tag.
			delete(clusterParams.Tags, metadataapi.TagClusterMaxDeletionDuration)

			networkClientFactory, err := tc.GetARMNetworkClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get ARM network client factory")
			subnetsClient := networkClientFactory.NewSubnetsClient()
			securityRulesClient := networkClientFactory.NewSecurityRulesClient()

			By("ensuring Red Hat delegation and cluster NSG on the vnet-integration (Swift) subnet before cluster create")
			integrationSubnetID, err := azcorearm.ParseResourceID(clusterParams.VnetIntegrationSubnetID)
			Expect(err).NotTo(HaveOccurred(), "failed to parse vnet-integration subnet resource ID %q", clusterParams.VnetIntegrationSubnetID)
			Expect(integrationSubnetID.Parent).NotTo(BeNil(), "vnet-integration subnet resource ID %q has no parent virtual network", clusterParams.VnetIntegrationSubnetID)

			integrationSubnetResp, err := subnetsClient.Get(
				ctx,
				integrationSubnetID.ResourceGroupName,
				integrationSubnetID.Parent.Name,
				integrationSubnetID.Name,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get vnet-integration subnet %q", clusterParams.VnetIntegrationSubnetID)
			Expect(integrationSubnetResp.Properties).NotTo(BeNil(), "vnet-integration subnet Properties was nil")

			integrationSubnetResp.Properties.NetworkSecurityGroup = &armnetwork.SecurityGroup{
				ID: to.Ptr(clusterParams.NsgResourceID),
			}
			integrationSubnetResp.Properties.Delegations = []*armnetwork.Delegation{
				{
					Name: to.Ptr(aroHCPDelegationName),
					Properties: &armnetwork.ServiceDelegationPropertiesFormat{
						ServiceName: to.Ptr(customerNSGValidationDelegationServiceName),
					},
				},
			}

			subnetUpdatePoller, err := subnetsClient.BeginCreateOrUpdate(
				ctx,
				integrationSubnetID.ResourceGroupName,
				integrationSubnetID.Parent.Name,
				integrationSubnetID.Name,
				integrationSubnetResp.Subnet,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to begin updating vnet-integration subnet with NSG and Red Hat delegation")

			subnetUpdateCtx, subnetUpdateCancel := context.WithTimeout(ctx, 5*time.Minute)
			defer subnetUpdateCancel()
			_, err = subnetUpdatePoller.PollUntilDone(subnetUpdateCtx, &runtime.PollUntilDoneOptions{
				Frequency: framework.StandardPollInterval,
			})
			Expect(err).NotTo(HaveOccurred(), "failed waiting to update vnet-integration subnet with NSG and Red Hat delegation")

			By("creating the Swift HCP cluster with worker subnetId and vnetIntegrationSubnetId")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create Swift HCP cluster %q", customerClusterName)

			By("resolving worker and vnet-integration subnet CIDRs")
			workerCIDR := customerNSGValidationSubnetCIDR(ctx, subnetsClient, clusterParams.SubnetResourceID, "worker")
			swiftCIDR := customerNSGValidationSubnetCIDR(ctx, subnetsClient, clusterParams.VnetIntegrationSubnetID, "vnet-integration")
			GinkgoLogr.Info("resolved subnet CIDRs for NSG deny stuck-provisioning cases",
				"workerCIDR", workerCIDR,
				"swiftCIDR", swiftCIDR,
				"nsg", clusterParams.NsgName,
			)

			nodePoolClient := tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient()
			vmSize, err := tc.SelectVMSize(ctx, framework.DefaultWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a default worker VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())

			// --- Case 1: outbound Deny Any → worker (blocks kubelet → KAS ILB) ---
			By("Case 1: adding outbound Deny Any → worker subnet on the cluster NSG")
			customerNSGValidationCreateRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, outboundDenyRuleName, armnetwork.SecurityRule{
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Description:              to.Ptr("E2E: Deny Any to worker subnet so node pool stays stuck provisioning"),
					Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolAsterisk),
					SourceAddressPrefix:      to.Ptr("*"),
					SourcePortRange:          to.Ptr("*"),
					DestinationAddressPrefix: to.Ptr(workerCIDR),
					DestinationPortRanges: []*string{
						to.Ptr("443"),
						to.Ptr("6443"),
					},
					Access:    to.Ptr(armnetwork.SecurityRuleAccessDeny),
					Priority:  to.Ptr[int32](100),
					Direction: to.Ptr(armnetwork.SecurityRuleDirectionOutbound),
				},
			})

			By("Case 1: starting node pool create while outbound Deny Any→worker is present")
			customerNSGValidationBeginNodePoolCreate(ctx, tc, nodePoolClient, *resourceGroup.Name, customerClusterName, outboundNodePoolName, vmSize)

			By("Case 1: verifying the node pool stays stuck provisioning and never reaches Succeeded")
			customerNSGValidationExpectStuckProvisioning(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, outboundNodePoolName,
				"outbound Deny Any→worker", stuckProvisioningDuration, stuckProvisioningInterval)

			By("Case 1: removing outbound Deny rule before Case 1b")
			customerNSGValidationDeleteRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, outboundDenyRuleName)

			// --- Case 1b: outbound Deny Any→worker on all ports except 443/6443 (should still succeed) ---
			By("Case 1b: adding outbound Deny Any→worker on all ports except 443/6443")
			customerNSGValidationCreateRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, outboundDenyExceptKASRuleName, armnetwork.SecurityRule{
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Description:              to.Ptr("E2E: Deny outbound to worker except 443/6443; node pool should still succeed"),
					Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolAsterisk),
					SourceAddressPrefix:      to.Ptr("*"),
					SourcePortRange:          to.Ptr("*"),
					DestinationAddressPrefix: to.Ptr(workerCIDR),
					DestinationPortRanges: []*string{
						to.Ptr("1-442"),
						to.Ptr("444-6442"),
						to.Ptr("6444-65535"),
					},
					Access:    to.Ptr(armnetwork.SecurityRuleAccessDeny),
					Priority:  to.Ptr[int32](101),
					Direction: to.Ptr(armnetwork.SecurityRuleDirectionOutbound),
				},
			})
			DeferCleanup(func() {
				customerNSGValidationDeleteRule(context.WithoutCancel(ctx), securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, outboundDenyExceptKASRuleName)
			})

			By("Case 1b: starting node pool create while outbound Deny (except 443/6443) is present")
			customerNSGValidationBeginNodePoolCreate(ctx, tc, nodePoolClient, *resourceGroup.Name, customerClusterName, outboundExceptKASNodePoolName, vmSize)

			By("Case 1b: verifying the node pool reaches Succeeded when only non-KAS ports are denied")
			customerNSGValidationExpectNodePoolSucceeded(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, outboundExceptKASNodePoolName,
				"outbound Deny except 443/6443", nodePoolSuccessTimeout)

			By("Case 1b: removing outbound Deny-except-KAS rule before Case 2")
			customerNSGValidationDeleteRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, outboundDenyExceptKASRuleName)

			// --- Case 2: inbound Deny worker → Swift (blocks worker → private-router path) ---
			By("Case 2: adding inbound Deny worker → Swift subnet on the cluster NSG")
			customerNSGValidationCreateRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyRuleName, armnetwork.SecurityRule{
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Description:              to.Ptr("E2E: Deny worker to Swift so node pool stays stuck provisioning"),
					Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolAsterisk),
					SourceAddressPrefix:      to.Ptr(workerCIDR),
					SourcePortRange:          to.Ptr("*"),
					DestinationAddressPrefix: to.Ptr(swiftCIDR),
					DestinationPortRange:     to.Ptr("8443"),
					Access:                   to.Ptr(armnetwork.SecurityRuleAccessDeny),
					Priority:                 to.Ptr[int32](100),
					Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
				},
			})
			DeferCleanup(func() {
				customerNSGValidationDeleteRule(context.WithoutCancel(ctx), securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyRuleName)
			})

			By("Case 2: starting node pool create while inbound Deny worker→Swift is present")
			customerNSGValidationBeginNodePoolCreate(ctx, tc, nodePoolClient, *resourceGroup.Name, customerClusterName, inboundNodePoolName, vmSize)

			By("Case 2: verifying the node pool stays stuck provisioning and never reaches Succeeded")
			customerNSGValidationExpectStuckProvisioning(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, inboundNodePoolName,
				"inbound Deny worker→Swift", stuckProvisioningDuration, stuckProvisioningInterval)

			By("removing inbound Deny rule before Case 2b")
			customerNSGValidationDeleteRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyRuleName)

			// --- Case 2b: inbound Deny worker→Swift on all ports except 8443 (should still succeed) ---
			By("Case 2b: adding inbound Deny worker→Swift on all ports except 8443")
			customerNSGValidationCreateRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyExcept8443RuleName, armnetwork.SecurityRule{
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Description:              to.Ptr("E2E: Deny inbound worker to Swift except 8443; node pool should still succeed"),
					Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolAsterisk),
					SourceAddressPrefix:      to.Ptr(workerCIDR),
					SourcePortRange:          to.Ptr("*"),
					DestinationAddressPrefix: to.Ptr(swiftCIDR),
					DestinationPortRanges: []*string{
						to.Ptr("1-8442"),
						to.Ptr("8444-65535"),
					},
					Access:    to.Ptr(armnetwork.SecurityRuleAccessDeny),
					Priority:  to.Ptr[int32](101),
					Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
				},
			})
			DeferCleanup(func() {
				customerNSGValidationDeleteRule(context.WithoutCancel(ctx), securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyExcept8443RuleName)
			})

			By("Case 2b: starting node pool create while inbound Deny (except 8443) is present")
			customerNSGValidationBeginNodePoolCreate(ctx, tc, nodePoolClient, *resourceGroup.Name, customerClusterName, inboundExcept8443NodePoolName, vmSize)

			By("Case 2b: verifying the node pool reaches Succeeded when only non-8443 ports are denied")
			customerNSGValidationExpectNodePoolSucceeded(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, inboundExcept8443NodePoolName,
				"inbound Deny except 8443", nodePoolSuccessTimeout)

			By("Case 2b: removing inbound Deny-except-8443 rule before recovery create")
			customerNSGValidationDeleteRule(ctx, securityRulesClient, *resourceGroup.Name, clusterParams.NsgName, inboundDenyExcept8443RuleName)

			By("creating a node pool after Deny rules are removed")
			customerNSGValidationBeginNodePoolCreate(ctx, tc, nodePoolClient, *resourceGroup.Name, customerClusterName, recoveryNodePoolName, vmSize)

			By("verifying the node pool enters Provisioning after Deny rules are gone")
			customerNSGValidationExpectProvisioningState(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, recoveryNodePoolName)

			// RP forbids deleting the last node pool; best-effort delete cleans up extras,
			// then the final BeginDelete may return 400. Cluster teardown removes the rest.
			By("best-effort deleting all node pools after Deny rules are removed")
			for _, name := range []string{outboundNodePoolName, inboundNodePoolName, recoveryNodePoolName} {
				customerNSGValidationBestEffortDeleteNodePool(ctx, tc, *resourceGroup.Name, customerClusterName, name)
			}
		})
})

func customerNSGValidationSubnetCIDR(ctx context.Context, subnetsClient *armnetwork.SubnetsClient, subnetResourceID string, label string) string {
	parsed, err := azcorearm.ParseResourceID(subnetResourceID)
	Expect(err).NotTo(HaveOccurred(), "failed to parse %s subnet resource ID %q", label, subnetResourceID)
	Expect(parsed.Parent).NotTo(BeNil(), "%s subnet resource ID %q has no parent virtual network", label, subnetResourceID)

	resp, err := subnetsClient.Get(ctx, parsed.ResourceGroupName, parsed.Parent.Name, parsed.Name, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to get %s subnet %q", label, subnetResourceID)
	Expect(resp.Properties).NotTo(BeNil(), "%s subnet Properties was nil", label)

	if resp.Properties.AddressPrefix != nil && *resp.Properties.AddressPrefix != "" {
		return *resp.Properties.AddressPrefix
	}
	Expect(resp.Properties.AddressPrefixes).NotTo(BeEmpty(), "%s subnet had no AddressPrefix or AddressPrefixes", label)
	Expect(resp.Properties.AddressPrefixes[0]).NotTo(BeNil(), "%s subnet AddressPrefixes[0] was nil", label)
	Expect(*resp.Properties.AddressPrefixes[0]).NotTo(BeEmpty(), "%s subnet AddressPrefixes[0] was empty", label)
	return *resp.Properties.AddressPrefixes[0]
}

func customerNSGValidationCreateRule(ctx context.Context, securityRulesClient *armnetwork.SecurityRulesClient, resourceGroupName string, nsgName string, ruleName string, rule armnetwork.SecurityRule) {
	rulePoller, err := securityRulesClient.BeginCreateOrUpdate(ctx, resourceGroupName, nsgName, ruleName, rule, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to begin creating NSG rule %q on NSG %q", ruleName, nsgName)

	ruleCtx, ruleCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer ruleCancel()
	_, err = rulePoller.PollUntilDone(ruleCtx, &runtime.PollUntilDoneOptions{
		Frequency: framework.StandardPollInterval,
	})
	Expect(err).NotTo(HaveOccurred(), "failed waiting for NSG rule %q on NSG %q to finish creating", ruleName, nsgName)
	GinkgoLogr.Info("created NSG security rule", "nsg", nsgName, "rule", ruleName)
}

func customerNSGValidationDeleteRule(ctx context.Context, securityRulesClient *armnetwork.SecurityRulesClient, resourceGroupName string, nsgName string, ruleName string) {
	deletePoller, err := securityRulesClient.BeginDelete(ctx, resourceGroupName, nsgName, ruleName, nil)
	if err != nil {
		GinkgoLogr.Info("BeginDelete NSG rule returned error (continuing)", "nsg", nsgName, "rule", ruleName, "error", err)
		return
	}
	deleteCtx, deleteCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer deleteCancel()
	_, err = deletePoller.PollUntilDone(deleteCtx, &runtime.PollUntilDoneOptions{
		Frequency: framework.StandardPollInterval,
	})
	if err != nil {
		GinkgoLogr.Info("waiting for NSG rule delete returned error (continuing)", "nsg", nsgName, "rule", ruleName, "error", err)
		return
	}
	GinkgoLogr.Info("deleted NSG security rule", "nsg", nsgName, "rule", ruleName)
}

func customerNSGValidationBeginNodePoolCreate(ctx context.Context, tc interface{ Location() string }, nodePoolClient *hcpsdk20251223preview.NodePoolsClient, resourceGroupName string, clusterName string, nodePoolName string, vmSize string) {
	nodePoolParams := framework.NewDefaultNodePoolParams20251223()
	nodePoolParams.ClusterName = clusterName
	nodePoolParams.NodePoolName = nodePoolName
	nodePoolParams.Replicas = int32(2)
	nodePoolParams.VMSize = vmSize
	// Avoid max-creation-duration forcing a terminal Failed during the stuck-provisioning window.
	delete(nodePoolParams.Tags, metadataapi.TagNodePoolMaxCreationDuration)

	nodePool := framework.BuildNodePoolFromParams20251223(nodePoolParams, tc.Location())
	_, err := nodePoolClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		clusterName,
		nodePoolName,
		nodePool,
		nil,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to begin creating node pool %q", nodePoolName)

	By("waiting for the node pool resource to become visible")
	Eventually(func(g Gomega) {
		_, getErr := framework.GetNodePool20251223(ctx, nodePoolClient, resourceGroupName, clusterName, nodePoolName)
		g.Expect(getErr).NotTo(HaveOccurred(), "GET node pool failed — RP may not have registered the resource yet")
	}, 2*time.Minute, 10*time.Second).Should(Succeed(),
		"timed out waiting for node pool %q to become visible after BeginCreateOrUpdate", nodePoolName)
}

func customerNSGValidationExpectStuckProvisioning(ctx context.Context, nodePoolClient *hcpsdk20251223preview.NodePoolsClient, resourceGroupName string, clusterName string, nodePoolName string, denyCaseLabel string, duration time.Duration, interval time.Duration) {
	var lastState hcpsdk20251223preview.ProvisioningState
	var lastErr string
	Consistently(func(g Gomega) {
		resp, getErr := framework.GetNodePool20251223(ctx, nodePoolClient, resourceGroupName, clusterName, nodePoolName)
		if getErr != nil {
			if msg := getErr.Error(); msg != lastErr {
				GinkgoLogr.Info("GET node pool returned error, skipping poll iteration", "error", getErr)
				lastErr = msg
			}
			return
		}
		lastErr = ""
		g.Expect(resp.Properties).NotTo(BeNil(), "node pool Properties was nil")
		g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "node pool Properties.ProvisioningState was nil")
		state := *resp.Properties.ProvisioningState
		if state != lastState {
			GinkgoLogr.Info("node pool provisioning state", "nodePool", nodePoolName, "state", state, "denyCase", denyCaseLabel)
			lastState = state
		}
		g.Expect(state).NotTo(Equal(hcpsdk20251223preview.ProvisioningStateSucceeded),
			"node pool %q reached Succeeded while %s was present; expected stuck provisioning", nodePoolName, denyCaseLabel)
	}, duration, interval).Should(Succeed(),
		"node pool %q reached Succeeded while %s NSG rule was present", nodePoolName, denyCaseLabel)
}

func customerNSGValidationExpectNodePoolSucceeded(ctx context.Context, nodePoolClient *hcpsdk20251223preview.NodePoolsClient, resourceGroupName string, clusterName string, nodePoolName string, caseLabel string, timeout time.Duration) {
	var lastState hcpsdk20251223preview.ProvisioningState
	Eventually(func(g Gomega) {
		resp, getErr := framework.GetNodePool20251223(ctx, nodePoolClient, resourceGroupName, clusterName, nodePoolName)
		g.Expect(getErr).NotTo(HaveOccurred(), "failed to GET node pool %q while waiting for Succeeded (%s)", nodePoolName, caseLabel)
		g.Expect(resp.Properties).NotTo(BeNil(), "node pool Properties was nil")
		g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "node pool Properties.ProvisioningState was nil")
		state := *resp.Properties.ProvisioningState
		if state != lastState {
			GinkgoLogr.Info("node pool provisioning state", "nodePool", nodePoolName, "state", state, "case", caseLabel)
			lastState = state
		}
		g.Expect(state).To(Equal(hcpsdk20251223preview.ProvisioningStateSucceeded),
			"node pool %q with %s should reach Succeeded; got %s", nodePoolName, caseLabel, state)
	}, timeout, 15*time.Second).Should(Succeed(),
		"timed out waiting for node pool %q to reach Succeeded with %s", nodePoolName, caseLabel)
}

func customerNSGValidationExpectProvisioningState(ctx context.Context, nodePoolClient *hcpsdk20251223preview.NodePoolsClient, resourceGroupName string, clusterName string, nodePoolName string) {
	var lastState hcpsdk20251223preview.ProvisioningState
	Eventually(func(g Gomega) {
		resp, getErr := framework.GetNodePool20251223(ctx, nodePoolClient, resourceGroupName, clusterName, nodePoolName)
		g.Expect(getErr).NotTo(HaveOccurred(), "failed to GET node pool %q while waiting for Provisioning", nodePoolName)
		g.Expect(resp.Properties).NotTo(BeNil(), "node pool Properties was nil")
		g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "node pool Properties.ProvisioningState was nil")
		state := *resp.Properties.ProvisioningState
		if state != lastState {
			GinkgoLogr.Info("node pool provisioning state after Deny cleanup", "nodePool", nodePoolName, "state", state)
			lastState = state
		}
		g.Expect(state).NotTo(Equal(hcpsdk20251223preview.ProvisioningStateFailed),
			"node pool %q reached Failed after Deny rules were removed; expected to enter Provisioning", nodePoolName)
		// Accept Succeeded if create races past Provisioning quickly; require at least Provisioning.
		g.Expect(state).To(Or(
			Equal(hcpsdk20251223preview.ProvisioningStateProvisioning),
			Equal(hcpsdk20251223preview.ProvisioningStateSucceeded),
		), "node pool %q should enter Provisioning after Deny rules were removed; got %s", nodePoolName, state)
	}, 5*time.Minute, 15*time.Second).Should(Succeed(),
		"timed out waiting for node pool %q to enter Provisioning after Deny rules were removed", nodePoolName)
}

func customerNSGValidationBestEffortDeleteNodePool(ctx context.Context, tc interface {
	Get20251223ClientFactoryOrDie(ctx context.Context) *hcpsdk20251223preview.ClientFactory
}, resourceGroupName string, clusterName string, nodePoolName string) {
	nodePoolClient := tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient()
	deleteCtx, cancel := context.WithTimeout(ctx, framework.NodePoolDeletionTimeout)
	defer cancel()

	poller, err := nodePoolClient.BeginDelete(deleteCtx, resourceGroupName, clusterName, nodePoolName, nil)
	if err != nil {
		GinkgoLogr.Info("BeginDelete node pool returned error (continuing)",
			"nodePool", nodePoolName, "cluster", clusterName, "error", err)
		return
	}
	_, err = poller.PollUntilDone(deleteCtx, &runtime.PollUntilDoneOptions{
		Frequency: framework.StandardPollInterval,
	})
	if err != nil {
		GinkgoLogr.Info("waiting for node pool delete returned error (continuing)",
			"nodePool", nodePoolName, "cluster", clusterName, "error", err)
		return
	}
	GinkgoLogr.Info("deleted node pool", "nodePool", nodePoolName)
}
