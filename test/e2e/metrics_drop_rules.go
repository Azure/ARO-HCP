// Copyright 2025 Microsoft Corporation
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type dropRule struct {
	Component    string
	Regex        string
	SourceLabels []string
}

type prometheusResponse struct {
	Status string         `json:"status"`
	Data   prometheusData `json:"data"`
}

type prometheusData struct {
	Result []prometheusResult `json:"result"`
}

type prometheusResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

var componentJobLabels = map[string]string{
	"etcd":                       "etcd",
	"kubeAPIServer":              "kube-apiserver",
	"kubeControllerManager":      "kube-controller-manager",
	"openshiftAPIServer":         "openshift-apiserver",
	"openshiftControllerManager": "openshift-controller-manager",
	"cvo":                        "cluster-version-operator",
}

func parseDropRules() ([]dropRule, error) {
	tmplData, err := TestArtifactsFS.ReadFile("test-artifacts/sre-metrics-set.configmap.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read configmap template: %w", err)
	}

	tmpl, err := template.New("configmap").Parse(string(tmplData))
	if err != nil {
		return nil, fmt.Errorf("failed to parse configmap template: %w", err)
	}

	data := map[string]any{
		"Values": map[string]any{
			"metricsSet": map[string]any{
				"performanceMetrics": false,
			},
		},
		"Release": map[string]any{
			"Namespace": "test",
		},
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("failed to render configmap template: %w", err)
	}

	var configMap struct {
		Data struct {
			Config string `json:"config"`
		} `json:"data"`
	}
	if err := yaml.Unmarshal(rendered.Bytes(), &configMap); err != nil {
		return nil, fmt.Errorf("failed to parse rendered configmap: %w", err)
	}

	var components map[string][]struct {
		Action       string   `json:"action"`
		Regex        string   `json:"regex"`
		SourceLabels []string `json:"sourceLabels"`
	}
	if err := yaml.Unmarshal([]byte(configMap.Data.Config), &components); err != nil {
		return nil, fmt.Errorf("failed to parse metrics set config: %w", err)
	}

	var rules []dropRule
	for component, entries := range components {
		for _, entry := range entries {
			if entry.Action != "drop" {
				continue
			}
			rules = append(rules, dropRule{
				Component:    component,
				Regex:        entry.Regex,
				SourceLabels: entry.SourceLabels,
			})
		}
	}
	return rules, nil
}

func dropRuleToPromQL(rule dropRule) string {
	if len(rule.SourceLabels) == 1 && rule.SourceLabels[0] == "__name__" {
		return fmt.Sprintf(`count({__name__=~"%s"})`, rule.Regex)
	}

	parts := strings.SplitN(rule.Regex, ";", len(rule.SourceLabels))
	if len(parts) != len(rule.SourceLabels) {
		return fmt.Sprintf(`count({__name__=~"%s"})`, rule.Regex)
	}

	metricName := parts[0]
	var labelMatchers []string
	for i := 1; i < len(rule.SourceLabels); i++ {
		labelMatchers = append(labelMatchers, fmt.Sprintf(`%s=~"%s"`, rule.SourceLabels[i], parts[i]))
	}
	return fmt.Sprintf(`count(%s{%s})`, metricName, strings.Join(labelMatchers, ","))
}

func amwLookupEndpoint(ctx context.Context, cred azcore.TokenCredential, subscriptionID, resourceGroup, workspaceName string) (string, error) {
	client, err := armmonitor.NewAzureMonitorWorkspacesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create monitor workspaces client: %w", err)
	}
	resp, err := client.Get(ctx, resourceGroup, workspaceName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get workspace %s: %w", workspaceName, err)
	}
	if resp.Properties == nil || resp.Properties.Metrics == nil || resp.Properties.Metrics.PrometheusQueryEndpoint == nil {
		return "", fmt.Errorf("workspace %s has no Prometheus query endpoint", workspaceName)
	}
	return *resp.Properties.Metrics.PrometheusQueryEndpoint, nil
}

func amwQuery(ctx context.Context, httpClient *http.Client, cred azcore.TokenCredential, endpoint, promQL string) (*prometheusResponse, error) {
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://prometheus.monitor.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Prometheus token: %w", err)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint URL %q: %w", endpoint, err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"

	params := url.Values{}
	params.Set("query", promQL)
	params.Set("time", fmt.Sprintf("%d", time.Now().Unix()))
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus query returned %d: %s", resp.StatusCode, string(body))
	}

	var promResp prometheusResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return nil, fmt.Errorf("failed to parse Prometheus response: %w", err)
	}
	if promResp.Status != "success" {
		return nil, fmt.Errorf("prometheus query error: %s", promResp.Status)
	}
	return &promResp, nil
}

var _ = Describe("SRE Metrics Set", func() {
	It("should not contain any series that the drop rules target",
		labels.RequireHappyPathInfra,
		labels.Low,
		labels.Positive,
		labels.DevelopmentOnly,
		labels.MIContainers(0),
		func(ctx context.Context) {
			regionRG := os.Getenv("AMW_REGION_RG")
			hcpWorkspace := os.Getenv("AMW_HCP_WORKSPACE_NAME")
			if regionRG == "" || hcpWorkspace == "" {
				Skip("AMW_REGION_RG and AMW_HCP_WORKSPACE_NAME must be set")
			}

			tc := framework.NewTestContext()

			By("parsing drop rules from the configmap template")
			rules, err := parseDropRules()
			Expect(err).NotTo(HaveOccurred(), "failed to parse drop rules from configmap template")
			Expect(rules).NotTo(BeEmpty(), "no drop rules found in configmap template")

			By("looking up the HCP AMW Prometheus endpoint")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credentials")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			endpoint, err := amwLookupEndpoint(ctx, cred, subscriptionID, regionRG, hcpWorkspace)
			Expect(err).NotTo(HaveOccurred(), "failed to look up AMW Prometheus endpoint")

			httpClient := &http.Client{Timeout: 30 * time.Second}

			for _, rule := range rules {
				jobLabel, hasJob := componentJobLabels[rule.Component]

				By(fmt.Sprintf("verifying drop rule for %s (regex: %s)", rule.Component, rule.Regex))

				if hasJob {
					By(fmt.Sprintf("waiting for up{job=%q} from %s", jobLabel, rule.Component))
					Eventually(func(g Gomega) {
						controlQuery := fmt.Sprintf(`up{job="%s"}`, jobLabel)
						resp, err := amwQuery(ctx, httpClient, cred, endpoint, controlQuery)
						g.Expect(err).NotTo(HaveOccurred(), "positive control query failed for %s", rule.Component)
						g.Expect(resp.Data.Result).NotTo(BeEmpty(), "up{job=%q} not yet present for %s — metrics may not be flowing", jobLabel, rule.Component)
					}).WithTimeout(10*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
						"timed out waiting for up{job=%q} for component %s", jobLabel, rule.Component)
				}

				By(fmt.Sprintf("asserting dropped series are absent: %s", dropRuleToPromQL(rule)))
				promQL := dropRuleToPromQL(rule)
				resp, err := amwQuery(ctx, httpClient, cred, endpoint, promQL)
				Expect(err).NotTo(HaveOccurred(), "drop rule absence query failed for %s", rule.Component)
				Expect(resp.Data.Result).To(BeEmpty(),
					"drop rule not working for %s: query %q returned results — series that should be dropped are still present",
					rule.Component, promQL)
			}
		})
})
