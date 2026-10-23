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
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/config"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
)

type amaPrometheusClient struct {
	httpClient *http.Client
	cred       azcore.TokenCredential
	endpoint   string
}

var _ = Describe("Engineering", func() {
	It("want metrics from SVC and MGMT clusters to be present in Azure Monitor WOrkspace",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.RequiresConfig,
		labels.MIContainers(0),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			serviceConfig, err := config.GetServiceConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to load service config")

			regionRGStr, err := config.GetStringByPath(serviceConfig, "regionRG")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve regionRG")

			svcWorkspaceNameStr, err := config.GetStringByPath(serviceConfig, "monitoring.svcWorkspaceName")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve monitoring.svcWorkspaceName")

			svcSubscriptionNameStr, err := config.GetStringByPath(serviceConfig, "svc.subscription.key")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve svc.subscription.key")

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			subscriptionsClientFactory, err := tc.GetARMSubscriptionsClientFactory()
			Expect(err).NotTo(HaveOccurred(), "failed to get ARM subscriptions client factory")

			subscriptionID, err := framework.GetSubscriptionID(ctx, subscriptionsClientFactory.NewClient(), svcSubscriptionNameStr)
			Expect(err).NotTo(HaveOccurred(), "failed to look up svc subscription ID for %q", svcSubscriptionNameStr)

			By("Resolving SVC workspace Prometheus endpoint")
			endpoint, err := promutil.LookupPrometheusEndpoint(ctx, cred, subscriptionID, regionRGStr, svcWorkspaceNameStr)
			Expect(err).NotTo(HaveOccurred(), "failed to look up SVC Prometheus endpoint for workspace %s in resource group %s", svcWorkspaceNameStr, regionRGStr)

			client := &amaPrometheusClient{
				httpClient: &http.Client{Timeout: 30 * time.Second},
				cred:       cred,
				endpoint:   endpoint,
			}

			type metricCheck struct {
				query       string
				description string
			}

			checks := []metricCheck{
				// --- SVC cluster services ---
				{`frontend_health`, "frontend health (SVC)"},
				{`frontend_http_requests_total`, "frontend HTTP request counts (SVC)"},
				{`backend_health`, "backend health (SVC)"},
				{`fleet_controller_health`, "fleet controller health (SVC)"},
				{`api_inbound_request_count`, "cluster-service inbound requests (SVC)"},
				{`maestro_build_info`, "maestro server build info (SVC)"},
				{`public_ip_count_by_region_service_tag`, "aro-hcp-exporter public IP count (SVC)"},
				{`process_start_time_seconds{namespace="aro-hcp-admin-api"}`, "admin API liveness (SVC)"},
				{`kubelet_running_pods{cluster=~".*svc.*"}`, "ensure kubelet metrics are present (SVC)"},

				// --- MGMT cluster services ---
				{`kube_applier_health`, "kube-applier health (MGMT)"},
				{`capacity_reporting_sync_errors_total`, "mgmt-agent capacity reporting sync errors (MGMT)"},
				{`hypershift_hostedclusters`, "hypershift hosted clusters gauge (MGMT)"},
				{`process_start_time_seconds{namespace="maestro", container="metrics-proxy"}`, "maestro agent liveness (MGMT)"},
				{`process_start_time_seconds{namespace="velero", container="velero"}`, "velero server liveness (MGMT)"},
				{`process_start_time_seconds{namespace="velero", container="node-agent"}`, "velero node-agent liveness (MGMT)"},
				{`kubelet_running_pods{cluster=~".*mgmt.*"}`, "ensure kubelet metrics are present (MGMT)"},

				// --- Shared infra (runs on SVC + MGMT) ---
				{`fluentbit_uptime`, "arobit forwarder uptime"},
			}

			// Track which metrics have been found so we don't re-query them.
			found := make(map[string]bool, len(checks))

			By("Polling Azure Monitor for service metrics from both clusters")
			Eventually(func(g Gomega) {
				now := time.Now()
				start := now.Add(-20 * time.Minute)

				var missing []string
				for _, c := range checks {
					if found[c.query] {
						continue
					}

					resp, err := promutil.QueryRange(ctx, client.httpClient, client.cred, client.endpoint, c.query, start, now, "60s")
					g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed for %s", c.description)
					if err != nil {
						return
					}

					if len(resp.Data.Result) > 0 {
						found[c.query] = true
						GinkgoLogr.Info("metric found", "metric", c.description)
					} else {
						missing = append(missing, c.description)
					}
				}

				if len(missing) > 0 {
					GinkgoLogr.Info("poll status",
						"found", len(found),
						"total", len(checks),
						"missingMetrics", strings.Join(missing, ", "))
				}

				g.Expect(missing).To(BeEmpty(),
					"expected %s but got no results", strings.Join(missing, "; "))
			}).WithTimeout(20*time.Minute).WithPolling(30*time.Second).WithContext(ctx).Should(Succeed(),
				"not all expected service metrics appeared in Azure Monitor")

			for _, c := range checks {
				GinkgoWriter.Printf("  [OK] %s\n", c.description)
			}
		})
})
