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
		name, previous, observed, evalTime string
		expected                           int
	}{
		{"missing poll pauses", "1 _x4", "1 stale", "4m", 1},
		{"skipped evaluations count once", "0 1 _x3", "_x4 1", "4m", 2},
		{"contrary observation resets", "0 2 _x3", "_x4 0", "4m", 0},
		{"third observation confirms", "0 2 _x3", "_x4 1", "4m", 3},
		{"confirmed count is bounded", "0 3 _x3", "_x4 1", "4m", 3},
		{"long gap pauses", "0 1 _x10", "_x11", "10m", 1},
		{"long gap resumes once", "0 1 _x10", "_x10 1", "10m", 2},
		{"long gap resumes the normal third confirmation", "0 2 _x10", "_x10 1", "10m", 3},
		{"latest reset survives a long gap", "2 0 _x10", "_x10 1", "10m", 1},
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
						"expr": "(" + expression + ") * 1", "eval_time": tc.evalTime,
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

func TestUpgradeMonitorReconcilesRestoredState(t *testing.T) {
	expressions := upgradeMonitorExpressions(t)
	keys := `cluster="svc-1", resource_id="/sub/1/rg/c1", subscription_id="sub-1"`
	for _, tc := range []struct {
		name, firing, stuck, current, completed, evalTime string
		wantFiring, wantStuck                             int
	}{
		{"unconfirmed completion holds restored firing", "0x35 1x4 _x20", "0x30 1x9 _x20", "0x2000", "_x51", "47m", 1, 1},
		{"confirmed completion clears without an extra recovery window", "0x35 1x4 _x20", "0x30 1x9 _x20", "0x2000", "_x47 1", "47m", 0, 0},
		{"younger incomplete target retains restored firing", "0x35 1x4 _x20", "0x30 1x9 _x20", "0x2000", "_x52", "51m", 1, 1},
		{"current healthy assessment preserves the latest clear value", "0x40 _x20", "0x40 _x20", "0x2000", "_x51", "50m", 0, 0},
		{"current stall can change a restored clear monitor", "0x40 _x20", "0x40 _x20", "1x2000", "_x52", "51m", 0, 1},
		{"an older firing value is not resurrected", "0x30 1x8 0 _x20", "0x30 1x8 0 _x20", "0x2000", "_x52", "51m", 0, 0},
		{"state is retained near the supported history limit", "0x35 1x4 _x1500", "0x30 1x9 _x1500", "0x2000", "_x1480", "1479m", 1, 1},
		{"new assessment cannot establish previous state outside the history bound", "0x35 1x4 _x1500", "0x30 1x9 _x1500", "0x2000", "_x1481", "1480m", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []map[string]string{
				{"series": "hosted_control_plane_upgrade:firing:bool{" + keys + "}", "values": tc.firing},
				{"series": "hosted_control_plane_upgrade:stuck:bool{" + keys + "}", "values": tc.stuck},
				{"series": "hosted_control_plane_upgrade:current_condition:bool{" + keys + "}", "values": tc.current},
				{"series": "hosted_control_plane_upgrade:completion_confirmed:info{" + keys + "}", "values": tc.completed},
				{"series": "hosted_control_plane_upgrade:target_observations_complete:bool{" + keys + "}", "values": "1x2000"},
				{"series": "backend_cluster_upgrade_monitoring_enabled{" + keys + "}", "values": "1x2000"},
			}
			checks := []map[string]interface{}{}
			for name, expected := range map[string]int{
				"hosted_control_plane_upgrade:firing:bool": tc.wantFiring,
				"hosted_control_plane_upgrade:stuck:bool":  tc.wantStuck,
			} {
				require.NotEmpty(t, expressions[name])
				checks = append(checks, map[string]interface{}{
					"expr": "(" + expressions[name] + ") * 1", "eval_time": tc.evalTime,
					"exp_samples": []map[string]interface{}{
						{"labels": "{" + keys + "}", "value": expected},
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
			file := filepath.Join(t.TempDir(), "restored-reconciliation_test.yaml")
			require.NoError(t, os.WriteFile(file, data, 0600))
			output, err := exec.Command("promtool", "test", "rules", file).CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func TestUpgradeClearConfirmationRequiresCurrentEvidence(t *testing.T) {
	expressions := upgradeMonitorExpressions(t)
	keys := `cluster="svc-1", resource_id="/sub/1/rg/c1", subscription_id="sub-1"`
	for _, tc := range []struct {
		name, count, observed string
		confirmed             bool
	}{
		{"third confirmation with current evidence", "3x50", "1x50", true},
		{"old confirmations without observations", "3x50", "_x51", false},
		{"old confirmations with contrary evidence", "3x50", "0x50", false},
		{"two observations are insufficient", "2x50", "1x50", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []map[string]string{}
			checks := []map[string]interface{}{}
			for reason, record := range map[string]string{
				"completed": "hosted_control_plane_upgrade:completion_confirmed:info",
				"lifecycle": "hosted_control_plane_upgrade:lifecycle_excluded:info",
			} {
				inputs = append(inputs,
					map[string]string{"series": "hosted_control_plane_upgrade:clear_confirmations:count{" + keys + `, reason="` + reason + `"}`, "values": tc.count},
					map[string]string{"series": "hosted_control_plane_upgrade:clear_observed:bool{" + keys + `, reason="` + reason + `"}`, "values": tc.observed},
				)
				expected := []map[string]interface{}{}
				if tc.confirmed {
					expected = append(expected, map[string]interface{}{"labels": "{" + keys + "}", "value": 1})
				}
				require.NotEmpty(t, expressions[record])
				checks = append(checks, map[string]interface{}{
					"expr": "(" + expressions[record] + ") * 1", "eval_time": "47m", "exp_samples": expected,
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
			file := filepath.Join(t.TempDir(), "current-clear-evidence_test.yaml")
			require.NoError(t, os.WriteFile(file, data, 0600))
			output, err := exec.Command("promtool", "test", "rules", file).CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func TestUpgradeRetirementWaitsForPublishedRecovery(t *testing.T) {
	expression := upgradeMonitorExpressions(t)["hosted_control_plane_upgrade:retired:info"]
	require.NotEmpty(t, expression)
	keys := `cluster="svc-1", resource_id="/sub/1/rg/c1", subscription_id="sub-1"`
	for _, tc := range []struct {
		name, state string
		retired     bool
	}{
		{"a firing monitor cannot retire before a clear is published", "1x50", false},
		{"published clear can retire", "0x50", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := []map[string]interface{}{}
			if tc.retired {
				expected = append(expected, map[string]interface{}{"labels": "{" + keys + "}", "value": 1})
			}
			fixture := map[string]interface{}{
				"evaluation_interval": "1m",
				"tests": []map[string]interface{}{{
					"interval": "1m",
					"input_series": []map[string]string{
						{"series": "hosted_control_plane_upgrade:clear_confirmations:count{" + keys + `, reason="lifecycle"}`, "values": "3x50"},
						{"series": "hosted_control_plane_upgrade:stuck:bool{" + keys + "}", "values": tc.state},
						{"series": `hosted_control_plane_upgrade:inventory_observations_complete:bool{cluster="svc-1", backend_process="backend-1"}`, "values": "1x50"},
					},
					"promql_expr_test": []map[string]interface{}{{
						"expr": "(" + expression + ") * 1", "eval_time": "47m", "exp_samples": expected,
					}},
				}},
			}
			data, err := yaml.Marshal(fixture)
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "retirement-recovery_test.yaml")
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
		{"gap exceeds instant lookback", "0x35 1x4 _x7", "0x30 1x9 _x7", "0x60", "47m", 1},
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
