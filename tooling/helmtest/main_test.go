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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/engine"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/types"

	"github.com/Azure/ARO-HCP/tooling/helmtest/testrunner"
)

func TestMgmtScheduling(t *testing.T) {
	raw, err := os.ReadFile("../../config/rendered/dev/dev/westus3.yaml")
	require.NoError(t, err)
	for _, component := range []string{"mgmt-agent", "kube-applier"} {
		t.Run(component, func(t *testing.T) {
			componentDir := filepath.Join("../..", component)
			valuesPath := filepath.Join(componentDir, "values.yaml")
			valuesSource, err := os.ReadFile(valuesPath)
			require.NoError(t, err)
			var rawValues yamlv3.Node
			require.NoError(t, yamlv3.Unmarshal(valuesSource, &rawValues))

			for _, test := range []struct {
				name        string
				role        string
				tolerations []corev1.Toleration
				override    bool
			}{
				{
					name: "default infra", role: "infra",
					tolerations: []corev1.Toleration{{Key: "infra", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
				{
					name: "system override", role: "system", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
				{
					name: "Exists toleration", role: "system", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}},
				},
				{
					name: "boolean role", role: "true", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
				{
					name: "null role", role: "null", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
				{
					name: "on role", role: "on", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
				{
					name: "numeric role", role: "123", override: true,
					tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}},
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					var cfg types.Configuration
					require.NoError(t, yaml.Unmarshal(raw, &cfg))
					if test.override {
						toleration := test.tolerations[0]
						cfg = types.MergeConfiguration(cfg, map[string]any{
							"mgmt": map[string]any{"scheduling": map[string]any{
								"role":          test.role,
								"tolerationKey": toleration.Key, "tolerationOperator": string(toleration.Operator),
								"tolerationValue": toleration.Value, "tolerationEffect": string(toleration.Effect),
							}},
						})
					}
					values, err := config.PreprocessFile(valuesPath, cfg)
					require.NoError(t, err)
					var overrides map[string]any
					require.NoError(t, yaml.Unmarshal(values, &overrides))
					chart, err := loader.Load(filepath.Join(componentDir, "deploy"))
					require.NoError(t, err)
					renderValues, err := util.ToRenderValues(chart, overrides, common.ReleaseOptions{
						Name: component, Namespace: component, IsInstall: true,
					}, nil)
					require.NoError(t, err)
					manifests, err := engine.Render(chart, renderValues)
					require.NoError(t, err)
					manifestPath := component + "/templates/deployment.yaml"
					require.Contains(t, manifests, manifestPath)
					var deployment appsv1.Deployment
					require.NoError(t, yaml.Unmarshal([]byte(manifests[manifestPath]), &deployment))
					pod := deployment.Spec.Template.Spec
					require.NotNil(t, pod.Affinity)
					require.NotNil(t, pod.Affinity.NodeAffinity)
					require.Equal(t, &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{
							Key: "aro-hcp.azure.com/role", Operator: corev1.NodeSelectorOpIn, Values: []string{test.role},
						}},
					}}}, pod.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
					require.Equal(t, test.tolerations, pod.Tolerations)
					require.Len(t, pod.Tolerations, 1)
					if test.override {
						require.NotEqual(t, "infra", pod.Tolerations[0].Key)
					}
				})
			}
		})
	}
}

func TestHelmTemplate(t *testing.T) {
	testrunner.RunTestHelmTemplate(t, "settings.yaml")
}

func TestACRValues(t *testing.T) {
	testrunner.RunTestACRValues(t, "settings.yaml")
}

func TestNodeRolloutConfig(t *testing.T) {
	testrunner.RunTestNodeRolloutConfig(t, "settings.yaml")
}

func TestShoeboxForwardUsesMDSDCompatibleTimestamp(t *testing.T) {
	fixture, err := os.ReadFile("../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_mgmt.yaml")
	if err != nil {
		t.Fatalf("failed to read Arobit management fixture: %v", err)
	}

	output := string(fixture)
	aliasIndex := strings.Index(output, "Alias           forward.shoebox")
	if aliasIndex == -1 {
		t.Fatal("Arobit management fixture does not contain the Shoebox Forward output")
	}

	output = output[aliasIndex:]
	blockEnd := strings.Index(output, "\n\n")
	if blockEnd != -1 {
		output = output[:blockEnd]
	}

	if !strings.Contains(output, "Retain_Metadata_In_Forward_Mode false") {
		t.Fatal("Shoebox Forward output must disable metadata retention for MDSD timestamp compatibility")
	}
}
