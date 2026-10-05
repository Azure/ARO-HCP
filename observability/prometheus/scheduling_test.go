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
	"bytes"
	"os"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
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
	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/config/types"
)

func TestResourcesTemplate(t *testing.T) {
	for _, cluster := range []string{"svc", "mgmt"} {
		tmpl, err := template.ParseFiles("values-" + cluster + ".yaml")
		require.NoError(t, err)
		for _, tc := range []struct {
			name  string
			input string
			want  string
		}{
			{
				name:  "both",
				input: `{"requests":{"cpu":1,"memory":"32Mi"},"limits":{"cpu":"50m","memory":"64Mi"}}`,
				want:  `{"requests":{"cpu":"1","memory":"32Mi"},"limits":{"cpu":"50m","memory":"64Mi"}}`,
			},
			{
				name:  "partial",
				input: `{"requests":{"cpu":"25m","memory":"NONE"},"limits":{"cpu":"unlimited","memory":"64Mi"}}`,
				want:  `{"requests":{"cpu":"25m"},"limits":{"memory":"64Mi"}}`,
			},
			{
				name:  "omit limits",
				input: `{"requests":{"cpu":"NONE","memory":"32Mi"},"limits":{"cpu":"NONE","memory":"unlimited"}}`,
				want:  `{"requests":{"memory":"32Mi"}}`,
			},
			{
				name:  "empty",
				input: `{"requests":{"cpu":"unlimited","memory":"NONE"},"limits":{"cpu":"NONE","memory":"unlimited"}}`,
				want:  `{}`,
			},
		} {
			t.Run(cluster+"/"+tc.name, func(t *testing.T) {
				var resources map[string]any
				require.NoError(t, yaml.Unmarshal([]byte(tc.input), &resources))
				var output bytes.Buffer
				require.NoError(t, tmpl.ExecuteTemplate(&output, "resources", resources))
				require.JSONEq(t, tc.want, output.String())
			})
		}
	}
}

func TestSchedulingRole(t *testing.T) {
	for _, cluster := range []string{"svc", "mgmt"} {
		valuesSource, err := os.ReadFile("values-" + cluster + ".yaml")
		require.NoError(t, err)
		var rawValues yamlv3.Node
		require.NoError(t, yamlv3.Unmarshal(valuesSource, &rawValues), "%s unrendered values must remain valid YAML for yamllint", cluster)
	}
	provider, err := config.NewConfigProvider("../../config/config.yaml")
	require.NoError(t, err)
	ev2, err := ev2config.ResolveConfig("public", "westus3")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement:       "dev",
		EnvironmentReplacement: "dev",
		RegionReplacement:      "westus3",
		RegionShortReplacement: "usw3",
		StampReplacement:       "1",
		Ev2Config:              ev2,
	})
	require.NoError(t, err)
	baseConfig, err := resolver.GetRegionConfiguration("westus3")
	require.NoError(t, err)
	type schedulingCase struct {
		name           string
		systemPoolOnly any
		role           string
	}
	for _, cluster := range []string{"svc", "mgmt", "opstool"} {
		cases := []schedulingCase{
			{"systemPoolOnly=false", false, "infra"},
			{"systemPoolOnly=true", true, "infra"},
		}
		if cluster == "svc" {
			cases[1].role = "system"
			// SDP preprocesses config leaves as non-empty strings before deployment.
			cases = append(cases, schedulingCase{"systemPoolOnly=placeholder", "__svc.aks.systemPoolOnly__", "infra"})
		}
		for _, tc := range cases {
			t.Run(cluster+"/"+tc.name, func(t *testing.T) {
				cfg := types.MergeConfiguration(baseConfig, map[string]any{
					"svc": map[string]any{"aks": map[string]any{"systemPoolOnly": tc.systemPoolOnly}},
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

				role := tc.role
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
					assert.Equal(t, want, affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution, path)
				}
			})
		}
	}
}
