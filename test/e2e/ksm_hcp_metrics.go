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
	"regexp"
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

			regionRG, err := config.ServiceConfig.GetByPath("regionRG")
			Expect(err).NotTo(HaveOccurred(), "failed to get regionRG from config")
			regionRGStr, ok := regionRG.(string)
			Expect(ok).To(BeTrue(), "regionRG is not a string")
			Expect(regionRGStr).NotTo(BeEmpty(), "regionRG is empty")

			hcpWorkspaceName, err := config.ServiceConfig.GetByPath("monitoring.hcpWorkspaceName")
			Expect(err).NotTo(HaveOccurred(), "failed to get hcpWorkspaceName from config")
			hcpWorkspaceNameStr, ok := hcpWorkspaceName.(string)
			Expect(ok).To(BeTrue(), "hcpWorkspaceName is not a string")
			Expect(hcpWorkspaceNameStr).NotTo(BeEmpty(), "hcpWorkspaceName is empty")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			By("Resolving HCP workspace Prometheus endpoint")
			endpoint, err := promutil.LookupPrometheusEndpoint(ctx, cred, subscriptionID, regionRGStr, hcpWorkspaceNameStr)
			Expect(err).NotTo(HaveOccurred(), "failed to look up HCP Prometheus endpoint")

			clusterName := e2eSetup.Cluster.Name
			query := fmt.Sprintf(`kube_node_info{hostedcontrolplane=~".*%s.*"}`, regexp.QuoteMeta(clusterName))

			httpClient := &http.Client{Timeout: 30 * time.Second}

			By("Polling Azure Monitor for kube_node_info metrics")
			// Azure Monitor Prometheus ingestion latency for new metric series can exceed 10 minutes.
			Eventually(func(g Gomega) {
				now := time.Now()
				start := now.Add(-5 * time.Minute)

				resp, err := promutil.QueryRange(ctx, httpClient, cred, endpoint, query, start, now, "60s")
				g.Expect(err).NotTo(HaveOccurred(), "Prometheus query_range failed")
				g.Expect(resp.Data.Result).NotTo(BeEmpty(),
					"expected kube_node_info metrics for cluster %q but got no results", clusterName)
			}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).WithContext(ctx).Should(Succeed(),
				"kube_node_info metrics never appeared in Azure Monitor for cluster %q", clusterName)
		})
})
