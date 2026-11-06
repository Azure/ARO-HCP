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

package internal

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

func upgradeMonitorExpressions(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "observability", "recording-rules", "userJourneyClusterUpgrade-recordingRule.yaml"))
	require.NoError(t, err)
	var rules struct {
		Spec struct {
			Groups []struct {
				Rules []struct {
					Record string `json:"record"`
					Expr   string `json:"expr"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(source, &rules))
	expressions := map[string]string{}
	for _, group := range rules.Spec.Groups {
		for _, rule := range group.Rules {
			expressions[rule.Record] = rule.Expr
		}
	}
	return expressions
}

func TestUpgradeClearConfirmationsCountObservationsNotElapsedGaps(t *testing.T) {
	expression := upgradeMonitorExpressions(t)["hosted_control_plane_upgrade:clear_confirmations:count"]
	require.NotEmpty(t, expression)
	for _, tc := range []struct {
		name, previous, observed string
		expected                 int
	}{
		{"missing poll pauses", "1 _x4", "1 stale", 1},
		{"skipped evaluations count once", "0 1 _x3", "_x4 1", 2},
		{"contrary observation resets", "0 2 _x3", "_x4 0", 0},
		{"third observation confirms", "0 2 _x3", "_x4 1", 3},
		{"confirmed count is bounded", "0 3 _x3", "_x4 1", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := `cluster="svc-1", resource_id="/sub/1/rg/c1", subscription_id="sub-1", reason="completed"`
			fixture := map[string]interface{}{
				"evaluation_interval": "1m",
				"tests": []map[string]interface{}{{
					"interval": "1m",
					"input_series": []map[string]string{
						{"series": "hosted_control_plane_upgrade:clear_confirmations:count{" + keys + "}", "values": tc.previous},
						{"series": "hosted_control_plane_upgrade:clear_observed:bool{" + keys + "}", "values": tc.observed},
					},
					"promql_expr_test": []map[string]interface{}{{
						"expr": "(" + expression + ") * 1", "eval_time": "4m",
						"exp_samples": []map[string]interface{}{
							{"labels": "{" + keys + "}", "value": tc.expected},
						},
					}},
				}},
			}
			data, err := yaml.Marshal(fixture)
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "clear-confirmations_test.yaml")
			require.NoError(t, os.WriteFile(file, data, 0600))
			output, err := exec.Command("promtool", "test", "rules", file).CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func TestUpgradeMonitorEstablishedLatchSurvivesEvaluationGap(t *testing.T) {
	expressions := upgradeMonitorExpressions(t)
	keys := `cluster="svc-1", resource_id="/sub/1/rg/c1", subscription_id="sub-1"`
	for _, tc := range []struct {
		name, firing, stuck, current, evalTime string
		expected                               int
	}{
		{"established latch", "0x35 1x8 _ _", "0x20 1x23 _ _", "0x60", "46m", 1},
		{"first firing evaluation", "0x35", "0x30 1x4", "0x30 1x10", "36m", 1},
		{"gap immediately after first firing", "0x35 1 _ _", "0x30 1x5 _ _", "0x30 1x5 _ 0x10", "38m", 1},
		{"pending clears at firing boundary", "0x35", "0x30 1x4", "0x60", "36m", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []map[string]string{
				{"series": "hosted_control_plane_upgrade:firing:bool{" + keys + "}", "values": tc.firing},
				{"series": "hosted_control_plane_upgrade:stuck:bool{" + keys + "}", "values": tc.stuck},
				{"series": "hosted_control_plane_upgrade:current_condition:bool{" + keys + "}", "values": tc.current},
				{"series": "backend_cluster_upgrade_monitoring_enabled{" + keys + "}", "values": "1x60"},
				{"series": "hosted_control_plane_upgrade:target_observations_complete:bool{" + keys + "}", "values": "1x60"},
				{"series": "backend_cluster_upgrade_target_info{" + keys + `, cluster_uuid="uuid-1", version="4.20.44"}`, "values": "1x60"},
				{"series": "backend_cluster_version_info{" + keys + `, cluster_uuid="uuid-1", version="4.20.44", state="partial"}`, "values": "1x60"},
				{"series": "hosted_control_plane_upgrade:duration_in_progress:seconds{" + keys + `, cluster_uuid="uuid-1", version="4.20.44", state="partial"}`, "values": "0x40 60+60x20"},
			}

			checks := []map[string]interface{}{}
			for _, name := range []string{"hosted_control_plane_upgrade:firing:bool", "hosted_control_plane_upgrade:stuck:bool"} {
				require.NotEmpty(t, expressions[name])
				checks = append(checks, map[string]interface{}{
					// Evaluate the production expression without filling skipped evaluations.
					"expr":      "(" + expressions[name] + ") * 1",
					"eval_time": tc.evalTime,
					"exp_samples": []map[string]interface{}{
						{"labels": "{" + keys + "}", "value": tc.expected},
					},
				})
			}
			fixture := map[string]interface{}{
				"evaluation_interval": "1m",
				"tests": []map[string]interface{}{{
					"interval": "1m", "input_series": inputs, "promql_expr_test": checks,
				}},
			}
			data, err := yaml.Marshal(fixture)
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "evaluation-gap_test.yaml")
			require.NoError(t, os.WriteFile(file, data, 0600))
			output, err := exec.Command("promtool", "test", "rules", file).CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}
