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

func (p *amaPrometheusClient) expectMetric(ctx context.Context, g Gomega, query, description string) {
	now := time.Now()
	// Azure Monitor Prometheus ingestion latency for new metric series can
	// exceed 10 minutes; a shorter lookback can miss samples that land with
	// an older timestamp once ingestion catches up.
	start := now.Add(-20 * time.Minute)

	resp, err := promutil.QueryRange(ctx, p.httpClient, p.cred, p.endpoint, query, start, now, "60s")
	g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed for %s", description)
	if err != nil {
		return
	}
	g.Expect(resp.Data.Result).NotTo(BeEmpty(), "expected %s metrics but got no results (query: %s)", description, query)
}

var _ = Describe("AMA Metrics", func() {
	It("service metrics from SVC and MGMT clusters should be present in Azure Monitor",
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

			// The SVC workspace lives in the mgmt (underlay infra) subscription, not the
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
				// SVC cluster services
				{`frontend_health`, "frontend health (SVC)"},
				{`backend_health`, "backend health (SVC)"},
				{`fleet_controller_health`, "fleet controller health (SVC)"},
				{`frontend_http_requests_total`, "frontend HTTP request counts (SVC)"},

				// MGMT cluster services
				{`kube_applier_health`, "kube-applier health (MGMT)"},
				{`maestro_build_info`, "maestro build info (MGMT)"},
				{`hypershift_hostedclusters`, "hypershift hosted clusters gauge (MGMT)"},
				{`hosted_cluster_managed_azure_info`, "mgmt-agent hosted cluster info (MGMT)"},
			}

			By("Polling Azure Monitor for service metrics from both clusters")
			Eventually(func(g Gomega) {
				for _, c := range checks {
					client.expectMetric(ctx, g, c.query, c.description)
				}
			}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).WithContext(ctx).Should(Succeed(),
				"not all expected service metrics appeared in Azure Monitor")

			for _, c := range checks {
				GinkgoWriter.Printf("  [OK] %s\n", c.description)
			}
		})
})
