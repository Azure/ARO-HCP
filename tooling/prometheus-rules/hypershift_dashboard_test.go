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

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

func TestHyperShiftDashboardQueries(t *testing.T) {
	raw, err := os.ReadFile("../../observability/grafana-dashboards/sre/user-journey/hypershift.json")
	require.NoError(t, err)
	var dashboard struct {
		Panels []struct {
			ID      int `json:"id"`
			Targets []struct {
				RefID string `json:"refId"`
				Expr  string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	require.NoError(t, json.Unmarshal(raw, &dashboard))
	queries := map[int]map[string]string{}
	for _, panel := range dashboard.Panels {
		queries[panel.ID] = map[string]string{}
		for _, target := range panel.Targets {
			t.Run(fmt.Sprintf("parse-panel-%d-%s", panel.ID, target.RefID), func(t *testing.T) {
				p := parser.NewParser(parser.Options{})
				expr, err := p.ParseExpr(target.Expr)
				require.NoError(t, err)
				parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
					selector, ok := node.(*parser.VectorSelector)
					if !ok {
						return nil
					}
					if strings.Contains(selector.Name, "backend_") {
						require.NotContains(t, target.Expr, "$cluster", "completed provisioning must remain regional")
						return nil
					}
					scoped := false
					for _, matcher := range selector.LabelMatchers {
						if matcher.Name == "cluster" && matcher.Type == labels.MatchRegexp && matcher.Value == "$cluster" {
							scoped = true
						}
					}
					require.True(t, scoped, "%s must respect the management cluster selection", selector.Name)
					return nil
				})
			})
			queries[panel.ID][target.RefID] = target.Expr
		}
	}

	var recordingFiles []string
	for _, source := range []string{
		"hypershiftUserJourney-recordingRule.yaml",
		"hypershiftControlPlaneOperator-recordingRule.yaml",
	} {
		raw, err := os.ReadFile(filepath.Join("../../observability/recording-rules", source))
		require.NoError(t, err)
		var rules monitoringv1.PrometheusRule
		require.NoError(t, yaml.Unmarshal(raw, &rules))
		for i := range rules.Spec.Groups {
			var recordings []monitoringv1.Rule
			for _, rule := range rules.Spec.Groups[i].Rules {
				if strings.HasSuffix(rule.Record, ":rate30d") {
					recordings = append(recordings, rule)
				}
			}
			rules.Spec.Groups[i].Rules = recordings
		}
		raw, err = yaml.Marshal(rules.Spec)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), source)
		require.NoError(t, os.WriteFile(path, raw, 0600))
		recordingFiles = append(recordingFiles, path)
	}

	type inputSeries struct {
		Series string `json:"series"`
		Values string `json:"values"`
	}
	createOperation := func(id, phase string, duration, age int) []inputSeries {
		var series []inputSeries
		for _, replica := range []string{"a", "b"} {
			labels := fmt.Sprintf(
				`{cluster="svc",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",operation_type="create",phase="%s",prometheus_replica="%s"}`,
				id, phase, replica,
			)
			for metric, value := range map[string]int{
				"backend_resource_operation_phase_info":                   1,
				"backend_resource_operation_last_transition_time_seconds": 3600 - age,
				"backend_resource_operation_start_time_seconds":           3600 - age - duration,
			} {
				series = append(series, inputSeries{
					Series: metric + labels,
					Values: fmt.Sprintf("%dx60", value),
				})
			}
		}
		return series
	}
	mixed := append(createOperation("fast", "succeeded", 600, 600), createOperation("slow", "succeeded", 1800, 600)...)
	mixed = append(mixed, createOperation("failed", "failed", 600, 600)...)
	mixed = append(mixed, createOperation("canceled", "canceled", 600, 600)...)
	mixed = append(mixed, createOperation("old-fast", "succeeded", 600, 259201)...)

	type sample struct {
		Labels string  `json:"labels"`
		Value  float64 `json:"value"`
	}
	type expressionTest struct {
		Expr     string   `json:"expr"`
		EvalTime string   `json:"eval_time"`
		Expected []sample `json:"exp_samples"`
	}
	type ruleTest struct {
		Interval string           `json:"interval"`
		Input    []inputSeries    `json:"input_series"`
		Queries  []expressionTest `json:"promql_expr_test"`
	}
	runQueries := func(t *testing.T, input []inputSeries, expressions []expressionTest) {
		t.Helper()
		expressions = append([]expressionTest(nil), expressions...)
		for i := range expressions {
			expressions[i].Expr = strings.ReplaceAll(expressions[i].Expr, "$cluster", ".*")
		}
		fixture := struct {
			RuleFiles []string   `json:"rule_files"`
			Interval  string     `json:"evaluation_interval"`
			Tests     []ruleTest `json:"tests"`
		}{
			RuleFiles: recordingFiles,
			Interval:  "1m",
			Tests: []ruleTest{{
				Interval: "1m",
				Input:    input,
				Queries:  expressions,
			}},
		}
		raw, err := yaml.Marshal(fixture)
		require.NoError(t, err)
		testFile := filepath.Join(t.TempDir(), "dashboard_test.yaml")
		require.NoError(t, os.WriteFile(testFile, raw, 0600))
		output, err := exec.Command("promtool", "test", "rules", testFile).CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	t.Run("management cluster selection scopes both workspaces and KAS context", func(t *testing.T) {
		var input []inputSeries
		for _, cluster := range []struct {
			name         string
			availability string
		}{
			{name: "test-mgmt-1", availability: "1"},
			{name: "test-mgmt-2", availability: "0.25"},
		} {
			for _, operator := range []struct {
				record     string
				namespace  string
				deployment string
				pod        string
			}{
				{record: "hypershift:operator:availability:ratio1m", namespace: "hypershift", deployment: "operator", pod: "operator-a"},
				{record: "hypershift:cpo:availability:ratio1m", namespace: "ocm-one", deployment: "control-plane-operator", pod: "control-plane-operator-a"},
			} {
				input = append(input,
					inputSeries{Series: fmt.Sprintf(`%s{cluster="%s",namespace="%s"}`, operator.record, cluster.name, operator.namespace), Values: cluster.availability + "x60"},
					inputSeries{Series: fmt.Sprintf(`up{cluster="%s",namespace="%s",pod="%s"}`, cluster.name, operator.namespace, operator.pod), Values: "1x60"},
					inputSeries{Series: fmt.Sprintf(`kube_deployment_spec_replicas{cluster="%s",namespace="%s",deployment="%s"}`, cluster.name, operator.namespace, operator.deployment), Values: "1x60"},
				)
			}
			input = append(input, inputSeries{
				Series: fmt.Sprintf(`hostedClusterAPI_kubeapiserver_available:ratio_avg_30d{cluster="%s",namespace="ocm-one"}`, cluster.name),
				Values: cluster.availability + "x60",
			})
		}
		for _, selection := range []struct {
			value        string
			availability float64
			kas          float64
		}{
			{value: "test-mgmt-1", availability: 100, kas: 100},
			{value: "test-mgmt-2", availability: 25, kas: 25},
			{value: "test-mgmt-1|test-mgmt-2", availability: 25, kas: 62.5},
			{value: ".*", availability: 25, kas: 62.5},
		} {
			t.Run(selection.value, func(t *testing.T) {
				var expressions []expressionTest
				for _, refID := range []string{"A", "B"} {
					expressions = append(expressions, expressionTest{
						Expr: strings.ReplaceAll(queries[34][refID], "$cluster", selection.value), EvalTime: "60m",
						Expected: []sample{{Labels: "{}", Value: selection.availability}},
					})
				}
				expressions = append(expressions, expressionTest{
					Expr: strings.ReplaceAll(queries[27]["A"], "$cluster", selection.value), EvalTime: "60m",
					Expected: []sample{{Labels: "{}", Value: selection.kas}},
				})
				runQueries(t, input, expressions)
			})
		}
	})
	t.Run("all panels remain unknown when telemetry is absent", func(t *testing.T) {
		var expressions []expressionTest
		for _, panel := range dashboard.Panels {
			for _, target := range panel.Targets {
				expressions = append(expressions, expressionTest{
					Expr:     target.Expr,
					EvalTime: "60m",
					Expected: []sample{},
				})
			}
		}
		runQueries(t, nil, expressions)
	})
	t.Run("node saturation requires complete current inputs", func(t *testing.T) {
		type metric struct {
			name    string
			healthy string
			hot     string
		}
		for _, gauge := range []struct {
			name    string
			panelID int
			labels  string
			metrics []metric
		}{
			{
				name: "CPU", panelID: 39, labels: `,cpu="0",mode="idle"`,
				metrics: []metric{{name: "node_cpu_seconds_total", healthy: "0+48", hot: "0+3"}},
			},
			{
				name: "memory", panelID: 40,
				metrics: []metric{
					{name: "node_memory_MemAvailable_bytes", healthy: "80", hot: "5"},
					{name: "node_memory_MemTotal_bytes", healthy: "100", hot: "100"},
				},
			},
			{
				name: "disk", panelID: 41, labels: `,device="/dev/sda",mountpoint="/",fstype="ext4"`,
				metrics: []metric{
					{name: "node_filesystem_avail_bytes", healthy: "80", hot: "5"},
					{name: "node_filesystem_size_bytes", healthy: "100", hot: "100"},
					{name: "node_filesystem_readonly", healthy: "0", hot: "0"},
				},
			},
		} {
			t.Run(gauge.name, func(t *testing.T) {
				expr := "round(" + queries[gauge.panelID]["A"] + ")"
				losses := []string{"all inputs", "node inventory", "retired node", "one HA replica"}
				for _, metric := range gauge.metrics {
					losses = append(losses, metric.name)
				}
				for _, loss := range losses {
					t.Run(loss, func(t *testing.T) {
						var input []inputSeries
						for _, node := range []string{"node-a", "node-b"} {
							inventory := "1x60"
							if loss == "node inventory" || (node == "node-b" && loss == "retired node") {
								inventory = "1x30 stale"
							}
							input = append(input, inputSeries{
								Series: fmt.Sprintf(`kube_node_info{cluster="test-mgmt-1",node="%s"}`, node), Values: inventory,
							})
							for _, metric := range gauge.metrics {
								value, suffix := metric.healthy, "x60"
								if node == "node-b" {
									value = metric.hot
									if loss == "all inputs" || loss == "retired node" || loss == "one HA replica" || loss == metric.name {
										suffix = "x30 stale"
									}
								}
								if loss != "node inventory" {
									input = append(input, inputSeries{
										Series: `kube_node_info{cluster="other-mgmt-1",node="node-b"}`, Values: "1x60",
									})
									for _, metric := range gauge.metrics {
										input = append(input, inputSeries{
											Series: fmt.Sprintf(`%s{cluster="other-mgmt-1",instance="node-b"%s}`, metric.name, gauge.labels),
											Values: metric.healthy + "x60",
										})
									}
								}
								labels := fmt.Sprintf(`{cluster="test-mgmt-1",instance="%s",prometheus_replica="a"%s}`, node, gauge.labels)
								input = append(input, inputSeries{Series: metric.name + labels, Values: value + suffix})
								if node == "node-b" && loss == "one HA replica" {
									labels := fmt.Sprintf(`{cluster="test-mgmt-1",instance="%s",prometheus_replica="b"%s}`, node, gauge.labels)
									input = append(input, inputSeries{Series: metric.name + labels, Values: value + "x60"})
								}
							}
						}
						var expected []sample
						switch loss {
						case "one HA replica":
							expected = []sample{{Labels: "{}", Value: 95}}
						case "retired node":
							expected = []sample{{Labels: "{}", Value: 20}}
						}
						runQueries(t, input, []expressionTest{
							{Expr: expr, EvalTime: "30m", Expected: []sample{{Labels: "{}", Value: 95}}},
							{Expr: expr, EvalTime: "31m", Expected: expected},
							{Expr: expr, EvalTime: "60m", Expected: expected},
						})
					})
				}
			})
		}
	})
	t.Run("disk saturation requires every expected filesystem", func(t *testing.T) {
		expr := "round(" + queries[41]["A"] + ")"
		for _, testCase := range []struct {
			name     string
			avail    string
			size     string
			readonly string
			expected []sample
		}{
			{name: "all mount inputs disappear", avail: "5x30 stale", size: "100x30 stale", readonly: "0x30 stale"},
			{name: "available bytes disappear", avail: "5x30 stale", size: "100x60", readonly: "0x60"},
			{name: "size disappears", avail: "5x60", size: "100x30 stale", readonly: "0x60"},
			{name: "read only status disappears", avail: "5x60", size: "100x60", readonly: "0x30 stale"},
			{
				name: "explicitly read only mounts remain excluded", avail: "5x30 stale", size: "100x60", readonly: "0x30 1x29",
				expected: []sample{{Labels: "{}", Value: 20}},
			},
			{
				name: "zero size mounts remain excluded", avail: "5x30 stale", size: "100x30 0x29", readonly: "0x60",
				expected: []sample{{Labels: "{}", Value: 20}},
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				input := []inputSeries{
					{Series: `kube_node_info{cluster="test-mgmt-1",node="node-a"}`, Values: "1x60"},
					{Series: `node_filesystem_avail_bytes{cluster="test-mgmt-1",instance="node-a",device="/dev/sda",mountpoint="/",fstype="ext4"}`, Values: "80x60"},
					{Series: `node_filesystem_size_bytes{cluster="test-mgmt-1",instance="node-a",device="/dev/sda",mountpoint="/",fstype="ext4"}`, Values: "100x60"},
					{Series: `node_filesystem_readonly{cluster="test-mgmt-1",instance="node-a",device="/dev/sda",mountpoint="/",fstype="ext4"}`, Values: "0x60"},
					{Series: `node_filesystem_avail_bytes{cluster="test-mgmt-1",instance="node-a",device="/dev/sdb",mountpoint="/data",fstype="ext4"}`, Values: testCase.avail},
					{Series: `node_filesystem_size_bytes{cluster="test-mgmt-1",instance="node-a",device="/dev/sdb",mountpoint="/data",fstype="ext4"}`, Values: testCase.size},
					{Series: `node_filesystem_readonly{cluster="test-mgmt-1",instance="node-a",device="/dev/sdb",mountpoint="/data",fstype="ext4"}`, Values: testCase.readonly},
				}
				for _, excluded := range []struct{ fstype, mountpoint string }{
					{fstype: "tmpfs", mountpoint: "/tmp"},
					{fstype: "devtmpfs", mountpoint: "/dev"},
					{fstype: "overlay", mountpoint: "/overlay"},
					{fstype: "squashfs", mountpoint: "/snap"},
					{fstype: "ext4", mountpoint: ""},
				} {
					input = append(input, inputSeries{
						Series: fmt.Sprintf(`node_filesystem_size_bytes{cluster="test-mgmt-1",instance="node-a",device="excluded",mountpoint="%s",fstype="%s"}`, excluded.mountpoint, excluded.fstype),
						Values: "100x60",
					})
				}
				runQueries(t, input, []expressionTest{
					{Expr: expr, EvalTime: "30m", Expected: []sample{{Labels: "{}", Value: 95}}},
					{Expr: expr, EvalTime: "31m", Expected: testCase.expected},
					{Expr: expr, EvalTime: "60m", Expected: testCase.expected},
				})
			})
		}
	})
	t.Run("pod capacity requires complete node reporting", func(t *testing.T) {
		expr := queries[42]["A"]
		require.NotEmpty(t, expr)
		for _, testCase := range []struct {
			name         string
			nodeB        string
			runningPodsB string
			nodeNameB    string
			expressions  []expressionTest
		}{
			{
				name:  "losing one kubelet makes healthy capacity unknown",
				nodeB: "node-b", runningPodsB: "90x30 stale", nodeNameB: "1x60",
				expressions: []expressionTest{
					{Expr: expr, EvalTime: "30m", Expected: []sample{{Labels: "{}", Value: 90}}},
					{Expr: expr, EvalTime: "31m", Expected: []sample{}},
					{Expr: expr, EvalTime: "60m", Expected: []sample{}},
				},
			},
			{
				name:  "missing node identity makes capacity unknown",
				nodeB: "node-b", runningPodsB: "90x60", nodeNameB: "1x30 stale",
				expressions: []expressionTest{
					{Expr: expr, EvalTime: "60m", Expected: []sample{}},
				},
			},
			{
				name:  "equal kubelet and capacity counts cannot hide a missing node",
				nodeB: "node-c", runningPodsB: "90x60", nodeNameB: "1x60",
				expressions: []expressionTest{
					{Expr: expr, EvalTime: "60m", Expected: []sample{}},
				},
			},
			{
				name:  "a reporting node with zero pods still provides coverage",
				nodeB: "node-b", runningPodsB: "0x60", nodeNameB: "1x60",
				expressions: []expressionTest{
					{Expr: expr, EvalTime: "60m", Expected: []sample{{Labels: "{}", Value: 45}}},
				},
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				runQueries(t, []inputSeries{
					{Series: `kube_node_status_allocatable{cluster="test-mgmt-1",node="node-a",resource="pods"}`, Values: "100x60"},
					{Series: `kube_node_status_allocatable{cluster="test-mgmt-1",node="node-b",resource="pods"}`, Values: "100x60"},
					{Series: `kubelet_running_pods{cluster="test-mgmt-1",instance="a:10250"}`, Values: "90x60"},
					{Series: `kubelet_running_pods{cluster="test-mgmt-1",instance="b:10250"}`, Values: testCase.runningPodsB},
					{Series: `kubelet_node_name{cluster="test-mgmt-1",instance="a:10250",node="node-a"}`, Values: "1x60"},
					{Series: fmt.Sprintf(`kubelet_node_name{cluster="test-mgmt-1",instance="b:10250",node="%s"}`, testCase.nodeB), Values: testCase.nodeNameB},
					// A complete cluster must not mask missing reporting in another cluster.
					{Series: `kube_node_status_allocatable{cluster="other-mgmt-1",node="node-b",resource="pods"}`, Values: "100x60"},
					{Series: `kubelet_running_pods{cluster="other-mgmt-1",instance="b:10250"}`, Values: "20x60"},
					{Series: `kubelet_node_name{cluster="other-mgmt-1",instance="b:10250",node="node-b"}`, Values: "1x60"},
				}, testCase.expressions)
			})
		}
	})
	t.Run("CPO startup grace uses existing scrape history", func(t *testing.T) {
		type observation struct {
			time             string
			ratio            float64
			budget           float64
			reporting        float64
			coverage         float64
			unknown          bool
			reportingUnknown bool
		}
		for _, testCase := range []struct {
			name         string
			record       string
			priorUp      string
			onlyNew      bool
			observations []observation
		}{
			{
				name:   "missing first recording is ignored until the five minute boundary",
				record: "_x61 1x9",
				observations: []observation{
					{time: "59m", ratio: 1, budget: 100, reporting: 100, coverage: 60},
					{time: "60m", reporting: 50, unknown: true},
					{time: "61m", ratio: 1, budget: 100, reporting: 100, coverage: 1},
				},
			},
			{
				name:   "early recordings are also excluded during startup grace",
				record: "_x55 0x15",
				observations: []observation{
					{time: "59m", ratio: 1, budget: 100, reporting: 100, coverage: 60},
					{time: "60m", ratio: 0, budget: -199900, reporting: 100, coverage: 6},
				},
			},
			{
				name:   "pod rotation and scrape gaps do not reset grace for a known HCP",
				record: "_x70", priorUp: "1x30 stale",
				observations: []observation{{time: "59m", reporting: 50, unknown: true}},
			},
			{
				name:   "an entirely new fleet does not manufacture healthy availability",
				record: "_x61 1x9", onlyNew: true,
				observations: []observation{{time: "59m", unknown: true, reportingUnknown: true}},
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				input := []inputSeries{
					{Series: `up{cluster="mc1",namespace="ocm-new",pod="control-plane-operator-new"}`, Values: "_x55 1x15"},
					{Series: `kube_deployment_spec_replicas{cluster="mc1",namespace="ocm-new",deployment="control-plane-operator"}`, Values: "_x55 1x15"},
					{Series: `hypershift:cpo:availability:ratio1m{cluster="mc1",namespace="ocm-new"}`, Values: testCase.record},
				}
				if !testCase.onlyNew {
					input = append(input,
						inputSeries{Series: `up{cluster="mc1",namespace="ocm-mature",pod="control-plane-operator-mature"}`, Values: "1x70"},
						inputSeries{Series: `kube_deployment_spec_replicas{cluster="mc1",namespace="ocm-mature",deployment="control-plane-operator"}`, Values: "1x70"},
						inputSeries{Series: `hypershift:cpo:availability:ratio1m{cluster="mc1",namespace="ocm-mature"}`, Values: "1x70"},
					)
				}
				if testCase.priorUp != "" {
					input = append(input, inputSeries{
						Series: `up{cluster="mc1",namespace="ocm-new",pod="control-plane-operator-retired"}`, Values: testCase.priorUp,
					})
				}
				var expressions []expressionTest
				for _, observed := range testCase.observations {
					for _, panelID := range []int{15, 19, 34, 35, 44, 48} {
						expr := queries[panelID]["B"]
						require.NotEmpty(t, expr)
						value := 100 * observed.ratio
						switch panelID {
						case 19:
							value = observed.ratio
						case 35:
							expr = "round(" + expr + ")"
							value = observed.budget
						case 44:
							expr = "round((" + expr + ") * 432)"
							value = observed.coverage
						case 48:
							value = observed.reporting
						}
						expected := []sample{{Labels: "{}", Value: value}}
						if observed.unknown && (panelID != 48 || observed.reportingUnknown) {
							expected = []sample{}
						}
						expressions = append(expressions, expressionTest{Expr: expr, EvalTime: observed.time, Expected: expected})
					}
				}
				runQueries(t, input, expressions)
			})
		}
	})
	for _, operator := range []struct {
		name       string
		namespace  string
		pod        string
		refID      string
		record     string
		deployment string
	}{
		{name: "operator", namespace: "hypershift", pod: "operator-a", refID: "A", record: "hypershift:operator:availability:ratio1m", deployment: "operator"},
		{name: "CPO", namespace: "ocm-one", pod: "control-plane-operator-a", refID: "B", record: "hypershift:cpo:availability:ratio1m", deployment: "control-plane-operator"},
	} {
		t.Run(operator.name+" availability requires independent inventory", func(t *testing.T) {
			for _, testCase := range []struct {
				name          string
				desiredB      string
				recordB       string
				upA           string
				upB           string
				unknown       bool
				healthyBefore bool
			}{
				{
					name:     "both deployment inputs disappear for one of two clusters",
					desiredB: "1x30 stale", recordB: "1x30 stale", upA: "1x60", upB: "1x30 0x29",
					unknown: true, healthyBefore: true,
				},
				{
					name:     "explicit scale to zero remains excluded",
					desiredB: "0x60", recordB: "1x30 stale", upA: "1x60", upB: "0x60",
				},
				{
					name:     "missing independent inventory cannot prove complete reporting",
					desiredB: "1x60", recordB: "1x60", upA: "1x30 stale", upB: "1x30 stale",
					unknown: true,
				},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					var input []inputSeries
					for _, cluster := range []struct {
						name    string
						desired string
						record  string
						up      string
					}{
						{name: "mc1", desired: "1x60", record: "1x60", up: testCase.upA},
						{name: "mc2", desired: testCase.desiredB, record: testCase.recordB, up: testCase.upB},
					} {
						deploymentLabels := fmt.Sprintf(`{cluster="%s",namespace="%s",deployment="%s"}`, cluster.name, operator.namespace, operator.deployment)
						recordLabels := fmt.Sprintf(`{cluster="%s"}`, cluster.name)
						if operator.refID == "B" {
							recordLabels = fmt.Sprintf(`{cluster="%s",namespace="%s"}`, cluster.name, operator.namespace)
						}
						input = append(input,
							inputSeries{Series: "kube_deployment_spec_replicas" + deploymentLabels, Values: cluster.desired},
							inputSeries{Series: "kube_deployment_status_replicas_available" + deploymentLabels, Values: cluster.desired},
							inputSeries{Series: operator.record + recordLabels, Values: cluster.record},
							inputSeries{Series: fmt.Sprintf(`up{cluster="%s",namespace="%s",pod="%s"}`, cluster.name, operator.namespace, operator.pod), Values: cluster.up},
						)
					}
					var expressions []expressionTest
					for _, panelID := range []int{15, 19, 34, 35, 44, 48} {
						expr := queries[panelID][operator.refID]
						require.NotEmpty(t, expr)
						expected := []sample{{Labels: "{}", Value: 100}}
						before := []sample{{Labels: "{}", Value: 100}}
						if panelID == 19 {
							expected[0].Value = 1
							before[0].Value = 1
						}
						if panelID == 44 {
							// Convert coverage back to observed minutes before comparing.
							expr = "round((" + expr + ") * 432)"
							expected[0].Value = 61
							before[0].Value = 31
						}
						if testCase.unknown {
							expected = []sample{}
						}
						expressions = append(expressions, expressionTest{Expr: expr, EvalTime: "60m", Expected: expected})
						if testCase.healthyBefore {
							expressions = append(expressions, expressionTest{Expr: expr, EvalTime: "30m", Expected: before})
							expressions = append(expressions, expressionTest{Expr: expr, EvalTime: "31m", Expected: []sample{}})
						}
					}
					runQueries(t, input, expressions)
				})
			}
		})
		t.Run(operator.name+" 30d reconciliation panels retain event weighting across HA replicas and counter resets", func(t *testing.T) {
			var input []inputSeries
			for _, workload := range []struct {
				cluster  string
				pod      string
				success  int
				errors   int
				fast     int
				total    int
				replicas int
			}{
				{cluster: "mc1", pod: operator.pod, success: 90, errors: 10, fast: 80, total: 100, replicas: 2},
				{cluster: "mc2", pod: operator.pod + "-other", errors: 10, total: 10, replicas: 1},
			} {
				for replica := range workload.replicas {
					labels := fmt.Sprintf(`cluster="%s",namespace="%s",pod="%s",prometheus_replica="%d"`,
						workload.cluster, operator.namespace, workload.pod, replica)
					for _, metric := range []struct {
						name      string
						increment int
					}{
						{fmt.Sprintf(`controller_runtime_reconcile_total{%s,result="success"}`, labels), workload.success},
						{fmt.Sprintf(`controller_runtime_reconcile_total{%s,result="error"}`, labels), workload.errors},
						{fmt.Sprintf(`controller_runtime_reconcile_time_seconds_bucket{%s,le="10"}`, labels), workload.fast},
						{fmt.Sprintf(`controller_runtime_reconcile_time_seconds_count{%s}`, labels), workload.total},
					} {
						input = append(input, inputSeries{
							Series: metric.name,
							Values: fmt.Sprintf("0+%dx29 0+%dx30", metric.increment, metric.increment),
						})
					}
				}
			}
			runQueries(t, input, []expressionTest{
				{Expr: "round(" + queries[16][operator.refID] + ")", EvalTime: "60m", Expected: []sample{{Labels: "{}", Value: 82}}},
				{Expr: "round(" + queries[17][operator.refID] + ")", EvalTime: "60m", Expected: []sample{{Labels: "{}", Value: 73}}},
			})
		})
		for _, budget := range []struct {
			name                        string
			panelID                     int
			numerator                   string
			denominator                 string
			healthyNumeratorIncrement   int
			retiredNumeratorIncrement   int
			retiredDenominatorIncrement int
		}{
			{
				name: "error", panelID: 36,
				numerator: `controller_runtime_reconcile_total{%s,result="error"}`, denominator: `controller_runtime_reconcile_total{%s,result="success"}`,
				retiredNumeratorIncrement: 10,
			},
			{
				name: "latency", panelID: 37,
				numerator: `controller_runtime_reconcile_time_seconds_bucket{%s,le="10"}`, denominator: `controller_runtime_reconcile_time_seconds_count{%s}`,
				healthyNumeratorIncrement: 10, retiredDenominatorIncrement: 10,
			},
		} {
			t.Run(operator.name+" "+budget.name+" budget", func(t *testing.T) {
				expr := queries[budget.panelID][operator.refID]
				require.NotEmpty(t, expr)
				labels := fmt.Sprintf(`namespace="%s",pod="%s"`, operator.namespace, operator.pod)
				current := []inputSeries{
					{Series: fmt.Sprintf(budget.numerator, labels), Values: fmt.Sprintf("0+%dx60", budget.healthyNumeratorIncrement)},
					{Series: fmt.Sprintf(budget.denominator, labels), Values: "0+10x60"},
				}
				stale := []inputSeries{
					{Series: current[0].Series, Values: fmt.Sprintf("0+%dx30 stale", budget.healthyNumeratorIncrement)},
					{Series: current[1].Series, Values: "0+10x30 stale"},
				}
				t.Run("healthy then stale inputs leave the budget unknown", func(t *testing.T) {
					runQueries(t, stale, []expressionTest{
						{Expr: expr, EvalTime: "30m", Expected: []sample{{Labels: "{}", Value: 100}}},
						{Expr: expr, EvalTime: "31m", Expected: []sample{}},
						{Expr: expr, EvalTime: "60m", Expected: []sample{}},
					})
				})
				for i, input := range stale {
					// The error counter itself also reports the unfiltered denominator.
					if budget.name == "error" && i == 1 {
						continue
					}
					t.Run(fmt.Sprintf("stale input %d leaves the budget unknown", i), func(t *testing.T) {
						partiallyStale := append([]inputSeries{}, current...)
						partiallyStale[i] = input
						runQueries(t, partiallyStale, []expressionTest{{
							Expr: expr, EvalTime: "60m", Expected: []sample{},
						}})
					})
				}
				t.Run("retired pods still contribute to the historical budget", func(t *testing.T) {
					retiredLabels := fmt.Sprintf(`namespace="%s",pod="%s-retired"`, operator.namespace, operator.pod)
					input := append([]inputSeries{}, current...)
					input = append(input,
						inputSeries{Series: fmt.Sprintf(budget.numerator, retiredLabels), Values: fmt.Sprintf("0+%dx30 stale", budget.retiredNumeratorIncrement)},
						inputSeries{Series: fmt.Sprintf(budget.denominator, retiredLabels), Values: fmt.Sprintf("0+%dx30 stale", budget.retiredDenominatorIncrement)},
					)
					runQueries(t, input, []expressionTest{{
						Expr: "(" + expr + ") < bool 0", EvalTime: "60m", Expected: []sample{{Labels: "{}", Value: 1}},
					}})
				})
			})
		}
	}
	for _, testCase := range []struct {
		name     string
		panelID  int
		refID    string
		input    []inputSeries
		expected []sample
		round    bool
		nan      bool
	}{
		{
			name:     "timeliness counts completed successes once and excludes canceled failed and expired operations",
			panelID:  5,
			input:    mixed,
			expected: []sample{{Labels: "{}", Value: 50}},
		},
		{
			name:     "all slow successful creates yield zero compliance rather than no data",
			panelID:  5,
			input:    createOperation("slow", "succeeded", 1800, 600),
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:     "no successful creates do not manufacture a healthy timeliness result",
			panelID:  5,
			input:    createOperation("canceled", "canceled", 600, 600),
			expected: []sample{},
		},
		{
			name:     "overspent timeliness budget remains negative",
			panelID:  47,
			input:    mixed,
			expected: []sample{{Labels: "{}", Value: -900}},
			round:    true,
		},
		{
			name:    "missing one HCP availability recording cannot leave the summary healthy",
			panelID: 15,
			refID:   "B",
			input: []inputSeries{
				{Series: `hypershift:cpo:availability:ratio1m{cluster="mc1",namespace="ocm-one"}`, Values: "1x60"},
				{Series: `kube_deployment_spec_replicas{cluster="mc1",namespace="ocm-one",deployment="control-plane-operator"}`, Values: "1x60"},
				{Series: `kube_deployment_spec_replicas{cluster="mc1",namespace="ocm-two",deployment="control-plane-operator"}`, Values: "1x60"},
				{Series: `up{cluster="mc1",namespace="ocm-one",pod="control-plane-operator-a"}`, Values: "1x60"},
				{Series: `up{cluster="mc1",namespace="ocm-two",pod="control-plane-operator-b"}`, Values: "1x60"},
			},
			expected: []sample{},
		},
		{
			name:    "known desired operator without availability telemetry reports zero coverage",
			panelID: 48,
			input: []inputSeries{
				{Series: `kube_deployment_spec_replicas{cluster="mc1",namespace="hypershift",deployment="operator"}`, Values: "3x60"},
				{Series: `up{cluster="mc1",namespace="hypershift",pod="operator-a"}`, Values: "1x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:    "reporting inventory with no waiting clusters has zero over limit",
			panelID: 11,
			input: []inputSeries{
				{Series: `hypershift_hostedclusters{cluster="mc1",platform="Azure"}`, Values: "2x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:    "reporting inventory with no waiting clusters has zero transitioning",
			panelID: 12,
			input: []inputSeries{
				{Series: `hypershift_hostedclusters{cluster="mc1",platform="Azure"}`, Values: "2x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:    "all reporting KAS clusters meeting the target has zero missing",
			panelID: 29,
			input: []inputSeries{
				{Series: `hostedClusterAPI_kubeapiserver_available:ratio_avg_30d{cluster="mc1",namespace="ocm-one"}`, Values: "1x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:    "all reporting KAS clusters missing the target has zero meeting",
			panelID: 28,
			input: []inputSeries{
				{Series: `hostedClusterAPI_kubeapiserver_available:ratio_avg_30d{cluster="mc1",namespace="ocm-one"}`, Values: "0.9x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
		},
		{
			name:    "zero reconciliation traffic leaves success undefined",
			panelID: 16,
			input: []inputSeries{
				{Series: `controller_runtime_reconcile_total{namespace="hypershift",pod="operator-a",result="success"}`, Values: "0x60"},
				{Series: `controller_runtime_reconcile_total{namespace="hypershift",pod="operator-a",result="error"}`, Values: "0x60"},
			},
			expected: []sample{{Labels: "{}", Value: 0}},
			nan:      true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			refID := testCase.refID
			if refID == "" {
				refID = "A"
			}
			expr := queries[testCase.panelID][refID]
			require.NotEmpty(t, expr)
			if testCase.round {
				expr = "round(" + expr + ")"
			}
			if testCase.nan {
				expr = "(" + expr + ") == bool (" + expr + ")"
			}
			runQueries(t, testCase.input, []expressionTest{{
				Expr:     expr,
				EvalTime: "60m",
				Expected: testCase.expected,
			}})
		})
	}
}
