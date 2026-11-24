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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestHypershiftMetrics(t *testing.T) {
	for _, mode := range []struct {
		name        string
		performance bool
	}{
		{name: "default"},
		{name: "performance", performance: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"}, internal.TestCase{
				Name:         "hypershift",
				Namespace:    "hypershift",
				Values:       "../../../hypershiftoperator/values.yaml",
				HelmChartDir: "../../../hypershiftoperator/deploy",
				TestData: map[string]any{
					"hypershift": map[string]any{"metricsSet": map[string]any{"performanceMetrics": mode.performance}},
				},
			})
			require.NoError(t, err)
			var config string
			for _, document := range strings.Split(manifest, "\n---") {
				var resource struct {
					Kind     string
					Metadata struct{ Name string }
					Data     map[string]string
				}
				require.NoError(t, yaml.Unmarshal([]byte(document), &resource))
				if resource.Kind == "ConfigMap" && resource.Metadata.Name == "sre-metric-set" {
					config = resource.Data["config"]
				}
			}
			require.NotEmpty(t, config, "rendered SRE metrics configuration")
			var components map[string][]struct {
				Action       string
				Regex        string
				SourceLabels []string
			}
			require.NoError(t, yaml.Unmarshal([]byte(config), &components))
			for _, component := range []string{"etcd", "kubeAPIServer", "kubeControllerManager", "openshiftAPIServer", "openshiftControllerManager", "cvo"} {
				t.Run(component, func(t *testing.T) {
					var keepRegexes []string
					for _, rule := range components[component] {
						if rule.Action == "keep" {
							require.Equal(t, []string{"__name__"}, rule.SourceLabels)
							keepRegexes = append(keepRegexes, rule.Regex)
						}
					}
					require.Len(t, keepRegexes, 1, "component must have one metric-name allowlist")
					// Prometheus relabel regexes match the entire input, not a substring.
					keep, err := regexp.Compile("^(?:" + keepRegexes[0] + ")$")
					require.NoError(t, err)
					retained := []string{"go_gc_duration_seconds", "go_gc_duration_seconds_sum", "go_gc_duration_seconds_count", "go_goroutines", "go_memstats_alloc_bytes", "process_cpu_seconds_total", "process_resident_memory_bytes"}
					var removed []string
					switch component {
					case "cvo":
						retained = append(retained, "cluster_operator_conditions")
						// cluster_version has no consumer; dropped in this profile.
						removed = []string{"cluster_version"}
					case "etcd":
						// Peer-status and traffic metrics needed to detect the
						// upstream etcd peerStatus latch race (etcd-io/etcd#22480,
						// OCPBUGS-142934): a peer latched inactive while traffic
						// still flows is only observable via etcd_network_* series.
						retained = append(retained,
							"etcd_network_active_peers",
							"etcd_network_disconnected_peers_total",
							"etcd_network_peer_sent_failures_total",
							"etcd_network_peer_sent_bytes_total",
							"etcd_network_peer_received_bytes_total",
							"etcd_server_has_leader",
							"etcd_server_proposals_applied_total",
						)
						removed = []string{
							// Only the _total variants of the peer byte counters
							// are allowlisted; unsuffixed names must not match.
							"etcd_network_peer_sent_bytes",
							"etcd_network_peer_received_bytes",
						}
					default:
						retained = append(retained, "rest_client_requests_total", "apiserver_client_certificate_expiration_seconds_bucket", "apiserver_client_certificate_expiration_seconds_count", "apiserver_client_certificate_expiration_seconds_sum")
						removed = []string{
							// All workqueue_.* is dropped, including the counters/gauges.
							"workqueue_adds_total",
							"workqueue_depth",
							"workqueue_retries_total",
							"workqueue_queue_duration_seconds_bucket",
							"workqueue_queue_duration_seconds_count",
							"workqueue_queue_duration_seconds_sum",
							"workqueue_work_duration_seconds_bucket",
							"workqueue_work_duration_seconds_count",
							"workqueue_work_duration_seconds_sum",
							"rest_client_request_duration_seconds_bucket",
							"rest_client_request_duration_seconds_count",
							"rest_client_request_duration_seconds_sum",
							// High-cardinality request histograms with no consumer.
							"apiserver_request_sli_duration_seconds_bucket",
							"apiserver_request_slo_duration_seconds_bucket",
							"apiserver_request_body_size_bytes_bucket",
						}
					}
					for _, metric := range removed {
						assert.False(t, keep.MatchString(metric), "%s must not be retained", metric)
					}
					for _, metric := range retained {
						assert.True(t, keep.MatchString(metric), "%s must be retained", metric)
						assert.False(t, keep.MatchString("unexpected_"+metric), "must not match a prefixed %s", metric)
						assert.False(t, keep.MatchString(metric+"_unexpected"), "must not match a suffixed %s", metric)
					}
				})
			}
		})
	}
}
