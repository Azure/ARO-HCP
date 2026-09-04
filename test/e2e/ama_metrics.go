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
	start := now.Add(-5 * time.Minute)

	resp, err := promutil.QueryRange(ctx, p.httpClient, p.cred, p.endpoint, query, start, now, "60s")
	g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed for %s", description)
	g.Expect(resp.Data.Result).NotTo(BeEmpty(), "expected %s metrics but got no results (query: %s)", description, query)
	GinkgoWriter.Printf("  [OK] %s: %d series\n", description, len(resp.Data.Result))
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

			regionRG, err := config.ServiceConfig.GetByPath("regionRG")
			Expect(err).NotTo(HaveOccurred(), "failed to get regionRG from config")
			regionRGStr, ok := regionRG.(string)
			Expect(ok).To(BeTrue(), "regionRG is not a string")

			svcWorkspaceName, err := config.ServiceConfig.GetByPath("monitoring.svcWorkspaceName")
			Expect(err).NotTo(HaveOccurred(), "failed to get monitoring.svcWorkspaceName from config")
			svcWorkspaceNameStr, ok := svcWorkspaceName.(string)
			Expect(ok).To(BeTrue(), "monitoring.svcWorkspaceName is not a string")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

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
		})
})
