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

	"github.com/Azure/ARO-HCP/test/util/config"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
)

var _ = Describe("KSM HCP Metrics", func() {
	It("kube_node_info metrics should be present in Azure Monitor for the happy-path cluster",
		labels.RequireHappyPathInfra,
		labels.Medium,
		labels.Positive,
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

			hcpWorkspaceNameStr, err := config.GetStringByPath(serviceConfig, "monitoring.hcpWorkspaceName")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve monitoring.hcpWorkspaceName")

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

			query := `ingresscontroller_info{hostedcontrolplane=~".+", container="kube-state-metrics"}`

			httpClient := &http.Client{Timeout: 30 * time.Second}

			By("Polling Azure Monitor for ingresscontroller_info metrics")
			// Azure Monitor Prometheus ingestion latency for new metric series can exceed 10 minutes.
			Eventually(func(g Gomega) {
				now := time.Now()
				// Ingestion latency (noted above) can exceed 10 minutes, so a
				// shorter lookback can miss samples that land with an older
				// timestamp once ingestion catches up.
				start := now.Add(-20 * time.Minute)

				resp, err := promutil.QueryRange(ctx, httpClient, cred, endpoint, query, start, now, "60s")
				g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed")
				if err != nil {
					return
				}
				g.Expect(resp.Data.Result).NotTo(BeEmpty(),
					"expected ingresscontroller_info metrics for at least one hostedcontrolplane but got no results")
			}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).WithContext(ctx).Should(Succeed(),
				"ingresscontroller_info metrics never appeared in Azure Monitor for any hostedcontrolplane")
		})
})
