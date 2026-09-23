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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/config"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
)

var _ = Describe("KSM HCP Metrics", func() {
	It("metrics should be present in HCP Azure Monitoring Workspace",
		labels.Medium,
		labels.Positive,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.RequiresConfig,
		labels.MIContainers(0),
		func(ctx context.Context) {
			const (
				customerClusterName  = "ksm-hcp-metrics"
				customerNodePoolName = "nodepool"
			)
			tc := framework.NewTestContext()

			serviceConfig, err := config.GetServiceConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to load service config")

			regionRGStr, err := config.GetStringByPath(serviceConfig, "regionRG")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve regionRG")

			hcpWorkspaceNameStr, err := config.GetStringByPath(serviceConfig, "monitoring.hcpWorkspaceName")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve monitoring.hcpWorkspaceName")

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "hcp-metrics", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for nodepool osDisk test")

			// creating cluster parameters
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

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q", customerNodePoolName)

			// The HCP workspace lives in the mgmt (underlay infra) subscription, not the
			// customer subscription that tc.SubscriptionID resolves to (the one the e2e
			// test's own HCP cluster is created in), so it must be looked up separately.
			mgmtSubscriptionNameStr, err := config.GetStringByPath(serviceConfig, "mgmt.subscription.key")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve mgmt.subscription.key")

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			subscriptionsClientFactory, err := tc.GetARMSubscriptionsClientFactory()
			Expect(err).NotTo(HaveOccurred(), "failed to get ARM subscriptions client factory")

			subscriptionID, err := framework.GetSubscriptionID(ctx, subscriptionsClientFactory.NewClient(), mgmtSubscriptionNameStr)
			Expect(err).NotTo(HaveOccurred(), "failed to look up mgmt subscription ID for %q", mgmtSubscriptionNameStr)

			By("Resolving HCP workspace Prometheus endpoint")
			endpoint, err := promutil.LookupPrometheusEndpoint(ctx, cred, subscriptionID, regionRGStr, hcpWorkspaceNameStr)
			Expect(err).NotTo(HaveOccurred(), "failed to look up HCP Prometheus endpoint")

			httpClient := &http.Client{Timeout: 30 * time.Second}

			metricList := []string{
				"ingresscontroller_info",
				"kube_node_status_condition",
				"kube_node_info",
			}

			By("Polling Azure Monitor Workspace for metrics")
			// Azure Monitor Prometheus ingestion latency for new metric series can exceed 10 minutes.
			Eventually(func(g Gomega) {
				for _, metric := range metricList {
					query := fmt.Sprintf(`%s{hostedcontrolplane=~".*%s.*", container="kube-state-metrics"}`, metric, customerClusterName)
					now := time.Now()
					start := now.Add(-20 * time.Minute)

					resp, err := promutil.QueryRange(ctx, httpClient, cred, endpoint, query, start, now, "60s")
					g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed")
					if err != nil {
						return
					}
					g.Expect(resp.Data.Result).NotTo(BeEmpty(),
						"expected %s metrics for at least one hostedcontrolplane but got no results", metric)

					GinkgoLogr.Info("found metrics for at least one hostedcontrolplane", "metric", metric, "results", resp.Data.Result)
				}
			}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).WithContext(ctx).Should(Succeed(),
				"not all metrics appeared in Azure Monitor for any hostedcontrolplane")
		})
})
