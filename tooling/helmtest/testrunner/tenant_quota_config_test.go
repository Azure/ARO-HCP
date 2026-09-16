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
	resolved, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, resolved, 0600))

	for _, values := range []string{"values.yaml.tmpl", "values.yaml"} {
		t.Run(values, func(t *testing.T) {
			manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: configPath}, internal.TestCase{
				Name:         "tenant-quota",
				Namespace:    "tenant-quota",
				Values:       filepath.Join(chartDir, values),
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
			require.Contains(t, pod.Spec.Containers[0].Env, corev1.EnvVar{
				Name: "AZURE_TOKEN_CREDENTIALS", Value: "WorkloadIdentityCredential",
			})
			if values == "values.yaml.tmpl" {
				local, err := config.PreprocessFile(filepath.Join(chartDir, "config.yaml.tmpl"), cfg)
				require.NoError(t, err)
				require.YAMLEq(t, string(local), cm.Data["config.yaml"])
			} else {
				var defaults map[string]any
				require.NoError(t, yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &defaults))
				require.Equal(t, map[string]any{"enabled": false}, defaults["ciJobOutcomes"])
			}
		})
	}
}
