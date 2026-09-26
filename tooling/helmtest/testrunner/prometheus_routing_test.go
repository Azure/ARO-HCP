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

package testrunner

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/stretchr/testify/require"
	yamlv2 "go.yaml.in/yaml/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestPrometheusWriteRelabelRouting(t *testing.T) {
	const (
		hcpNamespace   = "ocm-arohcpint-cluster-control-plane"
		temporaryLabel = "__tmp_arohcp_workspace"
		servicesURL    = "__dcrRemoteWriteUrl__"
		hcpsURL        = "https://hcps.example.com/api/v1/write"
	)
	values, err := os.ReadFile("../../../observability/prometheus/values-mgmt.yaml")
	require.NoError(t, err)

	for _, variant := range []struct {
		name, environment, hcpURL string
	}{
		{"split", "arohcpint", hcpsURL},
		{"no_hcp_destination", "arohcpint", "NONE"},
		{"no_environment", "", hcpsURL},
		{"no_environment_or_hcp_destination", "", "NONE"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			valuesPath := filepath.Join(t.TempDir(), "values.yaml")
			require.NoError(t, os.WriteFile(valuesPath, []byte(strings.ReplaceAll(string(values), "__hcpDcrRemoteWriteUrl__", variant.hcpURL)), 0600))
			manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"}, internal.TestCase{
				Name: "routing-test", Namespace: "prometheus", Values: valuesPath,
				HelmChartDir: "../../../observability/prometheus/deploy",
				TestData:     map[string]any{"clustersService": map[string]any{"environment": variant.environment}},
			})
			require.NoError(t, err)

			destinations := map[string][]*relabel.Config{}
			var jobLabel string
			var serviceLabels map[string]string
			agents := 0
			for _, document := range strings.Split(manifest, "\n---") {
				var resource struct {
					Kind     string
					Metadata struct {
						Labels map[string]string
					}
					Spec struct {
						JobLabel    string
						RemoteWrite []struct {
							URL                 string
							WriteRelabelConfigs []map[string]any
						}
					}
				}
				require.NoError(t, yaml.Unmarshal([]byte(document), &resource))
				if resource.Metadata.Labels["app.kubernetes.io/name"] == "kube-state-metrics" {
					switch resource.Kind {
					case "ServiceMonitor":
						require.Empty(t, jobLabel, "exactly one management KSM ServiceMonitor")
						jobLabel = resource.Spec.JobLabel
					case "Service":
						require.Nil(t, serviceLabels, "exactly one management KSM Service")
						serviceLabels = resource.Metadata.Labels
					}
				}
				if resource.Kind != "PrometheusAgent" {
					continue
				}
				agents++
				for _, destination := range resource.Spec.RemoteWrite {
					require.NotContains(t, destinations, destination.URL, "remote-write URLs must be unique")
					destinations[destination.URL] = nil
					if variant.environment == "" {
						require.Empty(t, destination.WriteRelabelConfigs, "empty environment preserves unfiltered remote write")
					} else {
						require.NotEmpty(t, destination.WriteRelabelConfigs, "configured environment must filter remote write")
					}
					for _, rule := range destination.WriteRelabelConfigs {
						// Translate only the Operator API field names; Prometheus supplies
						// defaults, regex compilation, validation, and execution.
						for camel, snake := range map[string]string{"sourceLabels": "source_labels", "targetLabel": "target_label"} {
							if value, ok := rule[camel]; ok {
								rule[snake] = value
								delete(rule, camel)
							}
						}
						encoded, err := yaml.Marshal(rule)
						require.NoError(t, err)
						var config relabel.Config
						require.NoError(t, yamlv2.UnmarshalStrict(encoded, &config))
						require.NoError(t, config.Validate(model.LegacyValidation))
						destinations[destination.URL] = append(destinations[destination.URL], &config)
					}
				}
			}
			require.Equal(t, 1, agents)
			require.NotEmpty(t, jobLabel, "management KSM must set ServiceMonitor jobLabel")
			ksmJob := serviceLabels[jobLabel]
			require.Equal(t, "kube-state-metrics", ksmJob, "routing relies on a stable job independent of Helm release name")
			require.Contains(t, destinations, servicesURL)
			if variant.hcpURL == "NONE" {
				require.Len(t, destinations, 1, "NONE disables the HCP destination, not services filtering")
			} else {
				require.Len(t, destinations, 2)
				require.Contains(t, destinations, hcpsURL)
			}

			type routingCase struct {
				name   string
				labels map[string]string
				hcp    bool
			}
			tests := []routingCase{
				{"API_server", map[string]string{"__name__": "apiserver_request_total", "job": "metrics", "namespace": hcpNamespace}, true},
				{"guest_KSM_promoted_namespace", map[string]string{"__name__": "kube_deployment_status_replicas_available", "job": "metrics", "namespace": "openshift-ingress-operator", "hostedcontrolplane": hcpNamespace}, true},
				{"guest_KSM_node", map[string]string{"__name__": "kube_node_info", "job": "metrics", "namespace": hcpNamespace, "hostedcontrolplane": hcpNamespace}, true},
				{"guest_KSM_no_namespace", map[string]string{"__name__": "kube_node_info", "job": "metrics", "hostedcontrolplane": hcpNamespace}, true},
				{"KSM_job_with_hostedcontrolplane", map[string]string{"__name__": "kube_deployment_status_replicas_available", "job": "kube-state-metrics", "namespace": "openshift-ingress-operator", "hostedcontrolplane": hcpNamespace}, true},
				{"KSM_job_with_both_HCP_labels", map[string]string{"__name__": "kube_node_info", "job": "kube-state-metrics", "namespace": hcpNamespace, "hostedcontrolplane": hcpNamespace}, true},
				{"KSM_empty_hostedcontrolplane", map[string]string{"__name__": "kube_pod_owner", "job": ksmJob, "namespace": hcpNamespace, "hostedcontrolplane": ""}, false},
				{"KSM_nonempty_other_hostedcontrolplane", map[string]string{"__name__": "kube_pod_owner", "job": ksmJob, "namespace": hcpNamespace, "hostedcontrolplane": "ocm-arohcpstg-cluster"}, true},
				{"KSM_job_prefix_is_not_management_KSM", map[string]string{"__name__": "kube_node_info", "job": "guest-kube-state-metrics", "namespace": hcpNamespace}, true},
				{"KSM_job_suffix_is_not_management_KSM", map[string]string{"__name__": "kube_node_info", "job": "kube-state-metrics-guest", "namespace": hcpNamespace}, true},
				{"missing_routing_labels", map[string]string{"__name__": "up"}, false},
				{"empty_routing_labels", map[string]string{"__name__": "up", "job": "", "namespace": "", "hostedcontrolplane": ""}, false},
				{"missing_job_HCP_namespace", map[string]string{"__name__": "up", "namespace": hcpNamespace}, true},
				{"missing_job_hostedcontrolplane", map[string]string{"__name__": "up", "hostedcontrolplane": hcpNamespace}, true},
				{"other_environment", map[string]string{"__name__": "up", "job": "metrics", "namespace": "ocm-arohcpstg-cluster", "hostedcontrolplane": "ocm-arohcpstg-cluster"}, false},
				{"prefix_not_substring", map[string]string{"__name__": "up", "namespace": "not-" + hcpNamespace, "hostedcontrolplane": "not-" + hcpNamespace}, false},
				{"namespace_OR_hostedcontrolplane", map[string]string{"__name__": "up", "namespace": hcpNamespace, "hostedcontrolplane": "ocm-arohcpstg-cluster"}, true},
				{"hostedcontrolplane_OR_namespace", map[string]string{"__name__": "up", "namespace": "ocm-arohcpstg-cluster", "hostedcontrolplane": hcpNamespace}, true},
			}
			for _, metric := range []string{
				"kube_deployment_spec_replicas", "kube_deployment_status_replicas_available",
				"kube_statefulset_replicas", "kube_statefulset_status_replicas_ready",
				"kube_pod_container_resource_requests", "kube_pod_owner", "kube_node_info",
				"hostedClusterAPI_kubeapiserver_available", "veleroBackup_phase", "future_ksm_metric",
			} {
				for _, namespace := range []string{"kube-system", hcpNamespace, ""} {
					sample := map[string]string{"__name__": metric, "job": ksmJob}
					if namespace != "" {
						sample["namespace"] = namespace
					}
					tests = append(tests, routingCase{"management_KSM/" + metric + "/" + namespace, sample, false})
				}
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					for _, initial := range []string{"", "services", "hcp", "unexpected"} {
						t.Run("temporary_label="+initial, func(t *testing.T) {
							sample := maps.Clone(test.labels)
							sample["cluster"] = "management-cluster"
							sample["region"] = "uksouth"
							sample["resource_id"] = "/subscriptions/test/resourceGroups/test"
							sample[temporaryLabel+"_unrelated"] = "preserve"
							if initial != "" {
								sample[temporaryLabel] = initial
							}
							input := labels.FromMap(sample)
							wantLabels := labels.NewBuilder(input)
							if variant.environment != "" {
								wantLabels.Del(temporaryLabel)
							}
							for url, rules := range destinations {
								builder := labels.NewBuilder(input)
								keep := relabel.ProcessBuilder(builder, rules...)
								wantKeep := variant.environment == "" || (url == hcpsURL) == test.hcp
								require.Equal(t, wantKeep, keep, "destination %s", url)
								if keep {
									require.Equal(t, wantLabels.Labels().String(), builder.Labels().String(), "destination %s must preserve every non-routing label", url)
								}
							}
						})
					}
				})
			}
		})
	}
}
