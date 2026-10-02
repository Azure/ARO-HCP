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
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestSwiftPodEnablement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, mode := range []string{"disabled", "audit", "enforce"} {
			t.Run(fmt.Sprintf("enabled=%t/mode=%s", enabled, mode), func(t *testing.T) {
				configuration := "mode: " + mode + "\n"
				manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"},
					internal.TestCase{Name: "swift-pod", Namespace: "mgmt-agent", HelmChartDir: "../../../mgmt-agent/deploy",
						Values: "../../../mgmt-agent/values.yaml", TestData: map[string]any{"mgmtAgent": map[string]any{
							"swiftPodMitigation": map[string]any{"enabled": enabled, "configuration": configuration}}}})
				require.NoError(t, err)
				var deploymentFound, configFound, roleFound, bindingFound, eviction, runtimeClass bool
				for _, document := range strings.Split(manifest, "\n---") {
					var kind metav1.TypeMeta
					require.NoError(t, yaml.Unmarshal([]byte(document), &kind))
					switch kind.Kind {
					case "Deployment":
						var deployment appsv1.Deployment
						require.NoError(t, yaml.Unmarshal([]byte(document), &deployment))
						if deployment.Name != "mgmt-agent" {
							continue
						}
						deploymentFound = true
						require.Equal(t, enabled, slices.Contains(deployment.Spec.Template.Spec.Containers[0].Args, "--swift-pod-mitigation-enabled=true"))
					case "ConfigMap":
						var cm corev1.ConfigMap
						require.NoError(t, yaml.Unmarshal([]byte(document), &cm))
						require.NotEqual(t, "swift-pod-mitigation-budget", cm.Name, "Helm must never manage durable accounting")
						if cm.Name == "mgmt-agent-swift-pod-mitigation" {
							configFound = true
							require.YAMLEq(t, configuration, cm.Data["config.yaml"])
						}
					case "ClusterRole":
						var role rbacv1.ClusterRole
						require.NoError(t, yaml.Unmarshal([]byte(document), &role))
						for _, rule := range role.Rules {
							if slices.Contains(rule.Resources, "runtimeclasses") {
								runtimeClass = true
								require.Equal(t, []string{"node.k8s.io"}, rule.APIGroups)
								require.Equal(t, []string{"get"}, rule.Verbs)
							}
							if slices.Contains(rule.Resources, "pods/eviction") {
								eviction = true
								require.Equal(t, []string{""}, rule.APIGroups)
								require.Equal(t, []string{"create"}, rule.Verbs)
							}
						}
					case "Role":
						var role rbacv1.Role
						require.NoError(t, yaml.Unmarshal([]byte(document), &role))
						if role.Name != "mgmt-agent-swift-pod-mitigation" {
							continue
						}
						roleFound = true
						require.Equal(t, "mgmt-agent", role.Namespace)
						require.Equal(t, []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"},
							ResourceNames: []string{"swift-pod-mitigation-budget"}, Verbs: []string{"get", "update"}}}, role.Rules)
					case "RoleBinding":
						var binding rbacv1.RoleBinding
						require.NoError(t, yaml.Unmarshal([]byte(document), &binding))
						if binding.Name != "mgmt-agent-swift-pod-mitigation" {
							continue
						}
						bindingFound = true
						require.Equal(t, "mgmt-agent-swift-pod-mitigation", binding.RoleRef.Name)
						require.Equal(t, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "mgmt-agent", Namespace: "mgmt-agent"}}, binding.Subjects)
					}
				}
				require.True(t, deploymentFound && configFound)
				require.Equal(t, enabled, roleFound)
				require.Equal(t, enabled, bindingFound)
				require.Equal(t, enabled, eviction)
				require.Equal(t, enabled, runtimeClass)
			})
		}
	}
}
