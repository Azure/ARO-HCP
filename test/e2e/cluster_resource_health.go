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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcehealth/armresourcehealth"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	// Availability status for HCP clusters is reported in production only.
	// Against the int or stage RP there is no availability data to read, so this
	// validation would fail for reasons unrelated to cluster health.
	//
	// Gating is therefore in two parts. StageAndProdOnly keeps the spec out of
	// the integration suites, and the runtime guard at the top of the body stops
	// it executing anywhere that is not production -- stage still selects it and
	// reports it as skipped. A dedicated prod-only label was considered and
	// rejected: a reusable label has to be honoured by every suite selector, and
	// that is a lot of shared machinery to maintain for one spec. See
	// test/AGENTS.md.
	//
	// In a development environment the suite also overrides
	// cloud.ResourceManager to point at the RP frontend directly
	// (per_invocation_framework.go), bypassing ARM entirely, so the cluster is
	// never an ARM resource at all and availabilityStatuses/current returns 404
	// no matter how healthy the cluster actually is. Never add
	// labels.AroRpApiCompatible here.
	//
	// The cost of this gating is that a regression surfaces only once it has
	// reached production.
	It("should see Available resource health for an HCP cluster with a node pool",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.StageAndProdOnly,
		labels.Slow,
		labels.MIContainers(1),
		labels.StartPriority(90),
		func(ctx context.Context) {
			// Guard before anything with a side effect -- no test context, no
			// identity assignment, no resource group -- so a non-production run
			// costs nothing. Log before Skip: Ginkgo stores the skip message
			// internally and the reporter only emits file:line, never the text.
			if !framework.IsProdDeployEnvironment() {
				skipMsg := fmt.Sprintf("resource health validation runs only in prod; %s is %q",
					leaseenv.RuntimeDeployEnvironment, os.Getenv(leaseenv.RuntimeDeployEnvironment))
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}

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
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("creating a single node node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(nodePoolReplicas)
			err = tc.CreateNodePoolFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q", customerNodePoolName)

			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			// The node health signal is derived from kube-state-metrics on the
			// worker, so confirm the node actually joined and went Ready first.
			// Without this a Resource Health timeout below is indistinguishable
			// from a node pool that never came up.
			By("verifying the node pool node is ready and schedulable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(ctx,
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
			clusterResp, err := framework.GetHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, customerClusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", customerClusterName)
			Expect(clusterResp.ID).NotTo(BeNil(), "HCP cluster %q response ID was nil", customerClusterName)
			resourceID := *clusterResp.ID

			By("building the Azure Resource Health client")
			healthClient, err := tc.NewAvailabilityStatusesClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create resource health client")

			By("polling Azure Resource Health until the cluster reports Available")
			// Bound the polling phase itself, so a request that stalls cannot run
			// past the health-check budget on the spec's much longer context. The
			// Eventually timeout alone cannot interrupt an in-flight call.
			healthCtx, cancelHealth := context.WithTimeout(ctx, resourceHealthAvailableTimeout)
			defer cancelHealth()

			var lastObserved string
			Eventually(func(g Gomega) error {
				status, err := framework.GetAvailabilityStatus(healthCtx, healthClient, resourceID)
				if err != nil {
					// A misconfigured subscription will never start answering, so
					// report it now instead of spending the rest of the budget
					// re-issuing a request that cannot succeed. Transient failures
					// and not-yet-observed resources still retry.
					if framework.IsPermanentConfigError(err) {
						return StopTrying(fmt.Sprintf("Azure Resource Health is not usable for %s", resourceID)).Wrap(err)
					}
					g.Expect(err).NotTo(HaveOccurred(), "failed to read Azure Resource Health for %s", resourceID)
				}

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
				return nil
			}).WithTimeout(resourceHealthAvailableTimeout).
				WithPolling(resourceHealthPollInterval).
				WithContext(healthCtx).
				Should(Succeed(), "Azure Resource Health never reported Available for %s; last observed %s", resourceID, lastObserved)

			GinkgoWriter.Printf("  [OK] resource health Available for %s\n", resourceID)
		})
})
