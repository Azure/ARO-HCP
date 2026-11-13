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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcehealth/armresourcehealth"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	// Prod only: availability status for HCP clusters is reported in production
	// only. Against the int or stage RP there is no availability data to read,
	// so the spec would fail for reasons unrelated to cluster health.
	//
	// labels.Slow is a speed bucket, not an environment gate -- it selects the
	// prod/parallel/slow suite over prod/parallel, and does nothing to keep the
	// spec away from int or stage. labels.ProdOnly is what does that.
	//
	// In a development environment the suite also overrides
	// cloud.ResourceManager to point at the RP frontend directly
	// (per_invocation_framework.go), bypassing ARM entirely, so the cluster is
	// never an ARM resource at all and availabilityStatuses/current returns 404
	// no matter how healthy the cluster actually is. Never add
	// labels.AroRpApiCompatible here.
	//
	// The cost of this gating is that a regression surfaces only once it has
	// reached production; see labels.ProdOnly.
	It("should see Available resource health for an HCP cluster with a node pool",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.ProdOnly,
		labels.Slow,
		labels.MIContainers(1),
		labels.StartPriority(90),
		func(ctx context.Context) {
			const (
				customerClusterName  = "resource-health"
				customerNodePoolName = "rh-workers"
				nodePoolReplicas     = 1

				// Availability status is populated asynchronously and is not
				// expected to appear as soon as the nodes are ready; the delay can
				// run to tens of minutes. Kept as a local literal because
				// test/AGENTS.md reserves constants.go for durations shared by
				// several specs.
				resourceHealthAvailableTimeout = 45 * time.Minute
				resourceHealthPollInterval     = time.Minute
			)

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "res-health", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for the resource health test")

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

			By("creating a single node node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(nodePoolReplicas)
			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q", customerNodePoolName)

			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			// The node health signal is derived from kube-state-metrics on the
			// worker, so confirm the node actually joined and went Ready first.
			// Without this a Resource Health timeout below is indistinguishable
			// from a node pool that never came up.
			By("verifying the node pool node is ready and schedulable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, nodePoolReplicas),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to verify the node pool has %d ready node", nodePoolReplicas)

			By("resolving the cluster ARM resource ID")
			clusterResp, err := framework.GetHCPCluster20240610(ctx, hcpClient, *resourceGroup.Name, customerClusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", customerClusterName)
			Expect(clusterResp.ID).NotTo(BeNil(), "HCP cluster %q response ID was nil", customerClusterName)
			resourceID := *clusterResp.ID

			By("building the Azure Resource Health client")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")
			healthClient, err := framework.NewAvailabilityStatusesClient(subscriptionID, cred)
			Expect(err).NotTo(HaveOccurred(), "failed to create resource health client")

			By("polling Azure Resource Health until the cluster reports Available")
			var lastObserved string
			Eventually(func(g Gomega) {
				status, err := framework.GetAvailabilityStatus(ctx, healthClient, resourceID)
				g.Expect(err).NotTo(HaveOccurred(), "failed to read Azure Resource Health for %s", resourceID)

				// Delta-only logging: a new line is emitted only when the observed
				// state changes, so the transition is visible without 45 minutes of
				// identical entries.
				observed := framework.FormatAvailabilityStatus(status)
				if observed != lastObserved {
					GinkgoLogr.Info("resource health observed", "resourceID", resourceID, "status", observed)
					lastObserved = observed
				}

				g.Expect(status.Properties).NotTo(BeNil(), "resource health response for %s had nil Properties", resourceID)
				g.Expect(status.Properties.AvailabilityState).NotTo(BeNil(), "resource health response for %s had nil AvailabilityState", resourceID)
				g.Expect(*status.Properties.AvailabilityState).To(Equal(armresourcehealth.AvailabilityStateValuesAvailable),
					"expected Resource Health Available for %s, observed %s", resourceID, observed)
			}).WithTimeout(resourceHealthAvailableTimeout).
				WithPolling(resourceHealthPollInterval).
				WithContext(ctx).
				Should(Succeed(), "Azure Resource Health never reported Available for %s; last observed %s", resourceID, lastObserved)

			GinkgoWriter.Printf("  [OK] resource health Available for %s\n", resourceID)
		})
})
