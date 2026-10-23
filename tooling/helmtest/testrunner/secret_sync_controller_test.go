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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestSecretSyncController(t *testing.T) {
	const configPath = "../../../config/rendered/dev/dev/westus3.yaml"
	const chartDir = "../../../secret-sync-controller/deploy"

	cfg, err := internal.LoadConfigAndMerge(configPath, nil)
	require.NoError(t, err)
	values, err := config.PreprocessFile(filepath.Join(chartDir, "values-azure.yaml"), cfg)
	require.NoError(t, err)

	for _, tc := range []struct {
		name         string
		namespace    string
		metricsPort  int32
		overridePort bool
	}{
		{name: "defaults", namespace: "secret-sync-controller", metricsPort: 8085},
		{name: "custom namespace and port", namespace: "custom-secret-sync", metricsPort: 18085, overridePort: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chartValues map[string]any
			require.NoError(t, yaml.Unmarshal(values, &chartValues))
			if tc.overridePort {
				chartValues["metricsPort"] = tc.metricsPort
			}
			customValues, err := yaml.Marshal(chartValues)
			require.NoError(t, err)
			valuesPath := filepath.Join(t.TempDir(), "values.yaml")
			require.NoError(t, os.WriteFile(valuesPath, customValues, 0600))

			manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: configPath}, internal.TestCase{
				Name:         "secret-sync-controller",
				Namespace:    tc.namespace,
				Values:       valuesPath,
				HelmChartDir: chartDir,
			})
			require.NoError(t, err)

			var deployment appsv1.Deployment
			var binding rbacv1.RoleBinding
			for _, document := range releaseutil.SplitManifests(manifest) {
				var object metav1.TypeMeta
				require.NoError(t, yaml.Unmarshal([]byte(document), &object))
				switch object.Kind {
				case "Deployment":
					require.NoError(t, yaml.Unmarshal([]byte(document), &deployment))
				case "RoleBinding":
					require.NoError(t, yaml.Unmarshal([]byte(document), &binding))
				}
			}
			require.Equal(t, tc.namespace, deployment.Namespace)
			var manager *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "manager" {
					manager = &deployment.Spec.Template.Spec.Containers[i]
				}
			}
			require.NotNil(t, manager)
			require.Equal(t, []string{
				"--provider-volume=/provider",
				"--token-request-audience=",
				fmt.Sprintf("--controller-server-address=:%d", tc.metricsPort),
				"--leader-election-namespace=" + tc.namespace,
				"--logging-format=json",
			}, manager.Args)
			require.Equal(t, []corev1.ContainerPort{{
				Name: "metrics", ContainerPort: tc.metricsPort, Protocol: corev1.ProtocolTCP,
			}}, manager.Ports)
			for name, probe := range map[string]*corev1.Probe{
				"startup": manager.StartupProbe, "liveness": manager.LivenessProbe, "readiness": manager.ReadinessProbe,
			} {
				require.NotNil(t, probe, "%s probe", name)
				require.NotNil(t, probe.HTTPGet, "%s HTTP probe", name)
				require.Equal(t, "/healthz", probe.HTTPGet.Path, "%s probe path", name)
				require.Equal(t, intstr.FromInt32(tc.metricsPort), probe.HTTPGet.Port, "%s probe port", name)
			}
			require.Equal(t, tc.namespace, binding.Namespace)
			require.Equal(t, rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "Role", Name: "secrets-store-sync-controller-leader-election-role",
			}, binding.RoleRef)
			require.Contains(t, binding.Subjects, rbacv1.Subject{
				Kind: "ServiceAccount", Name: deployment.Spec.Template.Spec.ServiceAccountName, Namespace: tc.namespace,
			})
		})
	}
}
