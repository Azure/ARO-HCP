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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestTenantQuotaCIJobOutcomes(t *testing.T) {
	const chartDir = "../../tenant-quota/deploy"

	provider, err := config.NewConfigProvider("../../../config/config-dev-ci.yaml")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement:       "dev",
		EnvironmentReplacement: "dev-ci",
		RegionReplacement:      "westus3",
		RegionShortReplacement: "usw3",
		StampReplacement:       "1",
	})
	require.NoError(t, err)
	cfg, err := resolver.GetRegionConfiguration("westus3")
	require.NoError(t, err)
	// The standalone schema describes layers, not the flattened runtime config.
	validateSchema := func(configuration map[string]any) error {
		return resolver.ValidateSchema(map[string]any{
			"clouds": map[string]any{"dev": map[string]any{"defaults": configuration}},
		})
	}
	require.NoError(t, validateSchema(cfg))
	resolved, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	for _, tc := range []struct {
		name             string
		values           string
		custom           bool
		omitStartupSince bool
	}{
		{name: "deployed", values: "values.yaml.tmpl"},
		{name: "defaults", values: "values.yaml"},
		{name: "custom controller settings", values: "values.yaml.tmpl", custom: true},
		{name: "omitted startup since", values: "values.yaml.tmpl", omitStartupSince: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var testConfig map[string]any
			require.NoError(t, yaml.Unmarshal(resolved, &testConfig))
			expected := map[string]any{
				"interval": "5m", "window": "24h", "repairInterval": "12h", "overlap": "3h",
				"workers": float64(10), "cacheSize": float64(20000), "cacheTTL": "15m",
				"startupSince": "",
			}
			if tc.custom {
				expected = map[string]any{
					"interval": "1m", "window": "48h", "repairInterval": "6h", "overlap": "2h",
					"workers": float64(3), "cacheSize": float64(100), "cacheTTL": "2m",
					"startupSince": "2026-09-15T00:00:00Z",
				}
				tenantQuota := testConfig["opstool"].(map[string]any)["tenantQuota"].(map[string]any)
				tenantQuota["exitOnPanic"] = true
				outcomes := tenantQuota["ciJobOutcomes"].(map[string]any)
				for key, value := range expected {
					outcomes[key] = value
				}
			}
			if tc.omitStartupSince {
				outcomes := testConfig["opstool"].(map[string]any)["tenantQuota"].(map[string]any)["ciJobOutcomes"].(map[string]any)
				delete(outcomes, "startupSince")
			}
			data, err := yaml.Marshal(testConfig)
			require.NoError(t, err)
			require.NoError(t, validateSchema(testConfig))
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(configPath, data, 0600))
			manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: configPath}, internal.TestCase{
				Name:         "tenant-quota",
				Namespace:    "tenant-quota",
				Values:       filepath.Join(chartDir, tc.values),
				HelmChartDir: chartDir,
			})
			require.NoError(t, err)

			var cm corev1.ConfigMap
			var deployment appsv1.Deployment
			for _, document := range releaseutil.SplitManifests(manifest) {
				var object metav1.TypeMeta
				require.NoError(t, yaml.Unmarshal([]byte(document), &object))
				switch object.Kind {
				case "ConfigMap":
					require.NoError(t, yaml.Unmarshal([]byte(document), &cm))
				case "Deployment":
					require.NoError(t, yaml.Unmarshal([]byte(document), &deployment))
				}
			}
			pod := deployment.Spec.Template
			require.Equal(t, "true", pod.Labels["azure.workload.identity/use"])
			require.Len(t, pod.Spec.Containers, 1)
			require.Equal(t, "1Gi", pod.Spec.Containers[0].Resources.Requests.Memory().String())
			require.Equal(t, "1Gi", pod.Spec.Containers[0].Resources.Limits.Memory().String())
			require.Contains(t, pod.Spec.Containers[0].Env, corev1.EnvVar{
				Name: "AZURE_TOKEN_CREDENTIALS", Value: "WorkloadIdentityCredential",
			})
			var runtime map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &runtime))
			require.Equal(t, tc.custom, runtime["exitOnPanic"])
			require.Equal(t, "30s", runtime["timeout"], "global collector timeout is unchanged")
			require.Equal(t, "24h", runtime["cacheTTL"], "global collector cache TTL is unchanged")
			outcomes := runtime["ciJobOutcomes"].(map[string]any)
			require.NotContains(t, outcomes, "timeout", "CI reconciles have no timeout")
			for key, value := range expected {
				require.Equal(t, value, outcomes[key], "controller setting %s", key)
			}
			if tc.values == "values.yaml.tmpl" {
				local, err := config.PreprocessFile(filepath.Join(chartDir, "config.yaml.tmpl"), testConfig)
				require.NoError(t, err)
				require.YAMLEq(t, string(local), cm.Data["config.yaml"])
			} else {
				expected["enabled"] = false
				require.Equal(t, expected, outcomes)
			}
		})
	}

	for _, tc := range []struct {
		field string
		value any
	}{
		{field: "workers", value: 0},
		{field: "cacheSize", value: 0},
		{field: "timeout", value: "10m"},
		{field: "startupSince", value: "not-a-date"},
		{field: "startupSince", value: "2026-09-31T00:00:00Z"},
		{field: "startupSince", value: "2026-09-15"},
	} {
		t.Run("schema rejects "+tc.field, func(t *testing.T) {
			var invalid map[string]any
			require.NoError(t, yaml.Unmarshal(resolved, &invalid))
			outcomes := invalid["opstool"].(map[string]any)["tenantQuota"].(map[string]any)["ciJobOutcomes"].(map[string]any)
			outcomes[tc.field] = tc.value
			err := validateSchema(invalid)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.field)
		})
	}
}
