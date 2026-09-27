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

package prometheus_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/types"
)

func TestSchedulingRole(t *testing.T) {
	valuesSource, err := os.ReadFile("values-svc.yaml")
	require.NoError(t, err)
	var rawValues yamlv3.Node
	require.NoError(t, yamlv3.Unmarshal(valuesSource, &rawValues), "unrendered values must remain valid YAML for yamllint")
	raw, err := os.ReadFile("../../config/rendered/dev/dev/westus3.yaml")
	require.NoError(t, err)
	for _, cluster := range []string{"svc", "mgmt", "opstool"} {
		for _, systemPoolOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/systemPoolOnly=%t", cluster, systemPoolOnly), func(t *testing.T) {
				var cfg types.Configuration
				require.NoError(t, yaml.Unmarshal(raw, &cfg))
				cfg = types.MergeConfiguration(cfg, map[string]any{
					"svc": map[string]any{"aks": map[string]any{"systemPoolOnly": systemPoolOnly}},
				})
				// Dev config has no opstool cluster; reuse its service images and AKS name.
				if cluster == "opstool" {
					cfg["opstool"] = cfg["svc"]
				}
				values, err := config.PreprocessFile("values-"+cluster+".yaml", cfg)
				require.NoError(t, err)
				var overrides map[string]any
				require.NoError(t, yaml.Unmarshal(values, &overrides))
				chart, err := loader.Load("deploy")
				require.NoError(t, err)
				require.NoError(t, chartutil.ProcessDependencies(chart, overrides))
				renderValues, err := util.ToRenderValues(chart, overrides, common.ReleaseOptions{
					Name: "prometheus", Namespace: "prometheus", IsInstall: true,
				}, nil)
				require.NoError(t, err)
				manifests, err := engine.Render(chart, renderValues)
				require.NoError(t, err)

				role := "infra"
				if cluster == "svc" && systemPoolOnly {
					role = "system"
				}
				want := &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: "aro-hcp.azure.com/role", Operator: corev1.NodeSelectorOpIn, Values: []string{role},
					}},
				}}}
				for _, path := range []string{
					"hcp-prometheus/templates/prometheus.yaml",
					"hcp-prometheus/charts/kube-prometheus-stack/templates/prometheus-operator/deployment.yaml",
				} {
					require.Contains(t, manifests, path)
					var resource struct {
						Kind string
						Spec struct {
							Affinity *corev1.Affinity
							Template struct{ Spec corev1.PodSpec }
						}
					}
					require.NoError(t, yaml.Unmarshal([]byte(manifests[path]), &resource))
					affinity := resource.Spec.Affinity
					if resource.Kind == "Deployment" {
						affinity = resource.Spec.Template.Spec.Affinity
					}
					require.NotNil(t, affinity, path)
					require.NotNil(t, affinity.NodeAffinity, path)
					require.Equal(t, want, affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution, path)
				}
			})
		}
	}
}
