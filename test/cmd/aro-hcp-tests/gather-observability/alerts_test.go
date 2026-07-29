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

package gatherobservability

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
)

func TestToAlert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input *armalertsmanagement.Alert
	}{
		{
			name:  "nil_properties",
			input: &armalertsmanagement.Alert{Name: to.Ptr("test-alert")},
		},
		{
			name: "nil_essentials",
			input: &armalertsmanagement.Alert{
				Name:       to.Ptr("test-alert"),
				Properties: &armalertsmanagement.AlertProperties{},
			},
		},
		{
			name:  "nil_name",
			input: &armalertsmanagement.Alert{},
		},
		{
			name: "full",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("full-alert"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity:                         to.Ptr(armalertsmanagement.SeveritySev2),
						AlertState:                       to.Ptr(armalertsmanagement.AlertStateNew),
						MonitorCondition:                 to.Ptr(armalertsmanagement.MonitorConditionFired),
						StartDateTime:                    to.Ptr(time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)),
						MonitorConditionResolvedDateTime: to.Ptr(time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)),
						Description:                      to.Ptr("Test description"),
						AlertRule:                        to.Ptr("/subscriptions/sub/providers/rules/myRule"),
						TargetResource:                   to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Monitor/accounts/myWorkspace"),
						SignalType:                       to.Ptr(armalertsmanagement.SignalTypeMetric),
					},
				},
			},
		},
		{
			name: "alertname_override",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("some-azure-guid-12345"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity: to.Ptr(armalertsmanagement.SeveritySev2),
					},
					Context: map[string]any{
						"labels": map[string]any{
							"alertname": "BackendControllerRetryHotLoop",
							"name":      "operationnodepooldelete",
						},
					},
				},
			},
		},
		{
			name: "no_alertname",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("azure-alert-id"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity: to.Ptr(armalertsmanagement.SeveritySev1),
					},
					Context: map[string]any{
						"labels": map[string]any{
							"severity": "warning",
						},
					},
				},
			},
		},
		{
			name: "with_annotations",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("some-guid"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity: to.Ptr(armalertsmanagement.SeveritySev3),
					},
					Context: map[string]any{
						"labels": map[string]any{
							"alertname": "MaestroRESTAPIErrorRate",
						},
						"annotations": map[string]any{
							"summary":     "Maestro REST API error rate above 5%",
							"description": "More than 5% of REST API requests are returning 5xx status codes.",
						},
					},
				},
			},
		},
		{
			name: "with_expression",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("some-guid"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity: to.Ptr(armalertsmanagement.SeveritySev3),
					},
					Context: map[string]any{
						"expression":      "rate(workqueue_retries_total[10m]) > 0.5",
						"expressionValue": "0.75",
						"labels": map[string]any{
							"alertname": "BackendControllerRetryHotLoop",
							"name":      "operationnodepoolcreate",
						},
						"annotations": map[string]any{
							"summary":     "Backend controller workqueue operationnodepoolcreate retry hot loop",
							"description": "Backend controller workqueue operationnodepoolcreate has a retry ratio of > 50%",
						},
					},
				},
			},
		},
		{
			name: "partial",
			input: &armalertsmanagement.Alert{
				Name: to.Ptr("partial-alert"),
				Properties: &armalertsmanagement.AlertProperties{
					Essentials: &armalertsmanagement.Essentials{
						Severity:   to.Ptr(armalertsmanagement.SeveritySev1),
						AlertState: to.Ptr(armalertsmanagement.AlertStateAcknowledged),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toAlert(tt.input)
			CompareWithFixture(t, got)
		})
	}
}

func TestAlertsPipeline(t *testing.T) {
	t.Parallel()

	parseTime := func(s string) *time.Time {
		parsed, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatalf("failed to parse time %q: %v", s, err)
		}
		return &parsed
	}

	rawAlerts := []*armalertsmanagement.Alert{
		{
			Name: to.Ptr("mise/MiseEnvoyScrapeDown"),
			Properties: &armalertsmanagement.AlertProperties{
				Essentials: &armalertsmanagement.Essentials{
					Severity:         to.Ptr(armalertsmanagement.SeveritySev4),
					AlertState:       to.Ptr(armalertsmanagement.AlertStateNew),
					MonitorCondition: to.Ptr(armalertsmanagement.MonitorConditionFired),
					StartDateTime:    parseTime("2026-04-13T06:47:46.0457216Z"),
					Description:      to.Ptr("Prometheus scrape for envoy-stats job in namespace mise is failing or missing."),
					AlertRule:        to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/mise"),
					TargetResource:   to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/microsoft.monitor/accounts/test-workspace"),
					SignalType:       to.Ptr(armalertsmanagement.SignalTypeMetric),
				},
				Context: map[string]any{
					"labels": map[string]any{
						"alertname": "MiseEnvoyScrapeDown",
						"cluster":   "test-svc",
						"severity":  "info",
					},
					"annotations": map[string]any{
						"correlationId": "MiseEnvoyScrapeDown/test-svc",
						"description":   "Prometheus scrape for envoy-stats job in namespace mise is failing or missing.",
						"info":          "Prometheus scrape for envoy-stats job in namespace mise is failing or missing.",
						"runbook_url":   "TBD",
						"summary":       "Envoy scrape target down for namespace=mise",
						"title":         "Envoy scrape target down for namespace=mise",
					},
					"expression": "group by (cluster) (up{job=\"kube-state-metrics\", cluster=~\".*-svc(-[0-9]+)?$\"}) unless on(cluster) group by (cluster) (up{endpoint=\"http-envoy-prom\", container=\"istio-proxy\", namespace=\"mise\"} == 1)",
				},
			},
		},
		{
			Name: to.Ptr("backend/BackendControllerRetryHotLoop"),
			Properties: &armalertsmanagement.AlertProperties{
				Essentials: &armalertsmanagement.Essentials{
					Severity:                         to.Ptr(armalertsmanagement.SeveritySev3),
					AlertState:                       to.Ptr(armalertsmanagement.AlertStateNew),
					MonitorCondition:                 to.Ptr(armalertsmanagement.MonitorConditionResolved),
					StartDateTime:                    parseTime("2026-04-13T05:36:11.5391795Z"),
					MonitorConditionResolvedDateTime: parseTime("2026-04-13T05:49:12.9148687Z"),
					Description:                      to.Ptr("Backend controller workqueue operationnodepooldelete has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work."),
					AlertRule:                        to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/backend"),
					TargetResource:                   to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/microsoft.monitor/accounts/test-workspace"),
					SignalType:                       to.Ptr(armalertsmanagement.SignalTypeMetric),
				},
				Context: map[string]any{
					"labels": map[string]any{
						"alertname": "BackendControllerRetryHotLoop",
						"cluster":   "test-svc",
						"name":      "operationnodepooldelete",
						"severity":  "critical",
					},
					"annotations": map[string]any{
						"correlationId": "BackendControllerRetryHotLoop/test-svc/operationnodepooldelete",
						"description":   "Backend controller workqueue operationnodepooldelete has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.",
						"info":          "Backend controller workqueue operationnodepooldelete has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.",
						"runbook_url":   "TBD",
						"summary":       "Backend controller workqueue operationnodepooldelete retry hot loop",
						"title":         "Backend controller workqueue operationnodepooldelete retry hot loop",
					},
					"expression": "( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace=\"aro-hcp\"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace=\"aro-hcp\"}[10m]) ) ) ) > 0.5",
				},
			},
		},
		{
			Name: to.Ptr("hcp-prometheus-rules/KubePodImagePull"),
			Properties: &armalertsmanagement.AlertProperties{
				Essentials: &armalertsmanagement.Essentials{
					Severity:         to.Ptr(armalertsmanagement.SeveritySev4),
					AlertState:       to.Ptr(armalertsmanagement.AlertStateNew),
					MonitorCondition: to.Ptr(armalertsmanagement.MonitorConditionFired),
					StartDateTime:    parseTime("2026-04-13T07:00:00Z"),
					AlertRule:        to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/hcp-prometheus-rules"),
					TargetResource:   to.Ptr("/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/hcp-underlay-test-rg/providers/microsoft.monitor/accounts/test-workspace"),
					SignalType:       to.Ptr(armalertsmanagement.SignalTypeMetric),
				},
				Context: map[string]any{
					"labels": map[string]any{
						"alertname": "KubePodImagePull",
						"cluster":   "test-svc",
						"namespace": "aro-hcp",
						"pod":       "frontend-abc123",
					},
					"expression": "max_over_time(kube_pod_container_status_waiting_reason{reason=\"ImagePullBackOff\", job=\"kube-state-metrics\"}[5m]) >= 1",
				},
			},
		},
	}

	var alerts []alert
	for _, raw := range rawAlerts {
		alerts = append(alerts, toAlert(raw))
	}

	knownIssues := mustParse(t, `knownIssues:
- name: "BackendControllerRetryHotLoop"
  reason: "known for delete controllers"
  labels:
    name: "operation.*delete"
- name: "MiseEnvoyScrapeDown"
  reason: "envoy scrape flaky during provisioning"
`)

	filtered := filterAlertsBySeverity(alerts, 4)
	classified := classifyAlerts(filtered, knownIssues)

	severityCounts := map[armalertsmanagement.Severity]int{}
	var knownCount int
	for _, a := range classified {
		severityCounts[a.Alert.Severity]++
		if a.Metadata.KnownIssue {
			knownCount++
		}
	}

	filterKeys, filterOptions := collectFilterOptions(classified)
	output := alertsOutput{
		Alerts: classified,
		Summary: alertsSummary{
			Total:      len(classified),
			Known:      knownCount,
			Unknown:    len(classified) - knownCount,
			BySeverity: severityCounts,
		},
		FilterKeys:    filterKeys,
		FilterOptions: filterOptions,
	}
	output.TimeWindow.Start = "2026-04-13T05:00:00Z"
	output.TimeWindow.End = "2026-04-13T08:00:00Z"

	CompareWithFixture(t, output)
}
