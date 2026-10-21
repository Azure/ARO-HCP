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
			} {
				t.Run(test.name, func(t *testing.T) {
					var cfg types.Configuration
					require.NoError(t, yaml.Unmarshal(raw, &cfg))
					if test.override {
						cfg = types.MergeConfiguration(cfg, map[string]any{
							"mgmt": map[string]any{"scheduling": map[string]any{
								"role": test.role,
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

// TestShoeboxForkRetainedOnMgmtMdsd guards the Geneva/Shoebox fork against the
// Kusto-routing simplification (docs/logging.md, spec §9 risk). The AllAroHcpLogs
// migration removes Kusto-routing filters from the forwarder but MUST retain the
// namespace_router + set_cluster_id Lua + shoebox fork, because customer logs
// reach Geneva through them. This path only runs on mgmt+mdsd, so the behavioural
// e2e may not exercise it — assert on the rendered mgmt+mdsd fixture instead.
func TestShoeboxForkRetainedOnMgmtMdsd(t *testing.T) {
	fixture, err := os.ReadFile("../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_mgmt.yaml")
	require.NoError(t, err)
	output := string(fixture)

	require.Contains(t, output, "Alias           filter.namespace_router",
		"namespace_router must be retained; the Shoebox fork matches the ocm.logs tag it produces")
	require.Contains(t, output, "Alias           lua.set_cluster_id_from_annotation",
		"set_cluster_id Lua must be retained; the Shoebox transform reuses the cluster_id it stamps")
	require.Contains(t, output, "Alias           filter.shoebox_router",
		"shoebox_router must render on mgmt+mdsd")
	require.Contains(t, output, "Alias           forward.shoebox",
		"the Shoebox forward output must render on mgmt+mdsd")
	require.Contains(t, output, "Host            127.0.0.1",
		"the Shoebox forward must target the MDSD clusterlogs sidecar host")
	require.Contains(t, output, "Port            5002",
		"the Shoebox forward must target the MDSD clusterlogs sidecar on port 5002")
}

// TestAllAroHcpLogsRawOutputRendered asserts the single AllAroHcpLogs landing
// output renders on both cluster types and targets rawLogs via the rawMapping
// (docs/logging.md). Exactly one such output must exist.
func TestAllAroHcpLogsRawOutputRendered(t *testing.T) {
	for _, fixture := range []string{
		"../../dev-infrastructure/zz_fixture_TestHelmTemplate_dev_westus3_svc_1_arobit.yaml",
		"../../dev-infrastructure/zz_fixture_TestHelmTemplate_dev_westus3_mgmt_1_arobit.yaml",
	} {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile(fixture)
			require.NoError(t, err)
			output := string(raw)
			require.Equal(t, 1, strings.Count(output, "Table_Name rawLogs"),
				"expected exactly one AllAroHcpLogs rawLogs output")
			require.Contains(t, output, "Database_Name AllAroHcpLogs",
				"rawLogs output must target the AllAroHcpLogs database")
			require.Contains(t, output, "ingestion_mapping_reference rawMapping",
				"rawLogs output must use the rawMapping ingestion mapping")
		})
	}
}

func TestArobitWatsonMDSDSocketConfiguration(t *testing.T) {
	for _, test := range []struct {
		name           string
		fixture        string
		mdsdRolePrefix string
	}{
		{
			name:           "management",
			fixture:        "../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_mgmt.yaml",
			mdsdRolePrefix: "/var/run/mdsd/",
		},
		{
			name:           "service",
			fixture:        "../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_svc.yaml",
			mdsdRolePrefix: "/var/run/mdsd/asa/",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := os.ReadFile(test.fixture)
			require.NoError(t, err)

			output := string(fixture)
			require.Contains(t, output, "MDSD_ROLE_PREFIX\n              value: \""+test.mdsdRolePrefix+"\"")
			require.NotContains(t, output, "MDSD_OPTION")
		})
	}
}
