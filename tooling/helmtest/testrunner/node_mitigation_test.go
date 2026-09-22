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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestNodeMitigationEnablement(t *testing.T) {
	for _, environment := range []string{"pers", "swft", "cspr", "dev", "ci00", "ci01", "perf", "int", "stg", "prod", "new-environment"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", environment, enabled), func(t *testing.T) {
				const configPath = "../../../config/rendered/dev/dev/westus3.yaml"
				testData := map[string]any{
					"environmentName": environment,
					"mgmtAgent": map[string]any{
						"nodeMitigation": map[string]any{"enabled": enabled},
					},
				}
				manifest, err := runTest(t.Context(), &internal.Settings{
					ConfigPath: configPath,
				}, internal.TestCase{
					Name: "node-mitigation", Namespace: "mgmt-agent",
					HelmChartDir: "../../../mgmt-agent/deploy", Values: "../../../mgmt-agent/values.yaml",
					TestData: testData,
				})
				require.NoError(t, err)
				cfg, err := internal.LoadConfigAndMerge(configPath, testData)
				require.NoError(t, err)
				parameters, err := config.PreprocessFile("../../../dev-infrastructure/configurations/mgmt-agent-permissions.tmpl.bicepparam", cfg)
				require.NoError(t, err)
				require.NotContains(t, string(parameters), "nodeMitigationEnabled",
					"SWIFT enablement must not provision Azure deletion permissions")
				var foundDeployment, foundRole, foundBudgetRole bool
				for _, document := range strings.Split(manifest, "\n---") {
					var kind metav1.TypeMeta
					require.NoError(t, yaml.Unmarshal([]byte(document), &kind))
					switch kind.Kind {
					case "Deployment":
						var deployment appsv1.Deployment
						require.NoError(t, yaml.Unmarshal([]byte(document), &deployment))
						for _, container := range deployment.Spec.Template.Spec.Containers {
							if container.Name != "mgmt-agent-controller" {
								continue
							}
							foundDeployment = true
							require.Equal(t, enabled, slices.Contains(container.Args, "--node-mitigation-enabled=true"))
						}
					case "ClusterRole":
						var role rbacv1.ClusterRole
						require.NoError(t, yaml.Unmarshal([]byte(document), &role))
						if role.Name != "mgmt-agent-controller" {
							continue
						}
						foundRole = true
						var eviction, daemonSetRead bool
						for _, rule := range role.Rules {
							eviction = eviction || slices.Contains(rule.Resources, "pods/eviction") && slices.Contains(rule.Verbs, "create")
							daemonSetRead = daemonSetRead || slices.Contains(rule.Resources, "daemonsets") && slices.Contains(rule.Verbs, "get")
						}
						require.Equal(t, enabled, eviction, "eviction permission must follow the enabled flag")
						require.False(t, daemonSetRead, "SWIFT must not grant disposable-agent reads")
					case "Role":
						var role rbacv1.Role
						require.NoError(t, yaml.Unmarshal([]byte(document), &role))
						if role.Name != "mgmt-agent-node-mitigation" {
							continue
						}
						foundBudgetRole = true
						var budgetWrites bool
						for _, rule := range role.Rules {
							budgetWrites = budgetWrites || slices.Contains(rule.Resources, "nodemitigationbudgets") && slices.Contains(rule.Verbs, "create")
						}
						require.Equal(t, enabled, budgetWrites, "budget writes must follow the enabled flag")
					}
				}
				require.True(t, foundDeployment && foundRole && foundBudgetRole, "controller and permission resources must be rendered")
			})
		}
	}
}
