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
			for _, component := range []string{"kubeAPIServer", "kubeControllerManager", "openshiftAPIServer", "openshiftControllerManager", "cvo"} {
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
					if component == "cvo" {
						retained = append(retained, "cluster_operator_conditions", "cluster_version")
					} else {
						retained = append(retained, "workqueue_adds_total", "workqueue_depth", "workqueue_retries_total", "rest_client_requests_total", "apiserver_client_certificate_expiration_seconds_bucket", "apiserver_client_certificate_expiration_seconds_count", "apiserver_client_certificate_expiration_seconds_sum")
						for _, removed := range []string{
							"workqueue_queue_duration_seconds_bucket",
							"workqueue_queue_duration_seconds_count",
							"workqueue_queue_duration_seconds_sum",
							"workqueue_work_duration_seconds_bucket",
							"workqueue_work_duration_seconds_count",
							"workqueue_work_duration_seconds_sum",
							"rest_client_request_duration_seconds_bucket",
							"rest_client_request_duration_seconds_count",
							"rest_client_request_duration_seconds_sum",
						} {
							assert.False(t, keep.MatchString(removed), "%s must not be retained", removed)
						}
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
