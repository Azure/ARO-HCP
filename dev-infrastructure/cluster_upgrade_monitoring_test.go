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

package infrastructure_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

type upgradeMonitoringTemplate struct {
	Parameters map[string]struct {
		DefaultValue  json.RawMessage `json:"defaultValue"`
		AllowedValues []string        `json:"allowedValues"`
	} `json:"parameters"`
	Resources json.RawMessage `json:"resources"`
}

type upgradeMonitoringResource struct {
	Name       string `json:"name"`
	Properties struct {
		Parameters map[string]json.RawMessage `json:"parameters"`
		Template   json.RawMessage            `json:"template"`
		Rules      []struct {
			Alert      string          `json:"alert"`
			Enabled    json.RawMessage `json:"enabled"`
			Expression string          `json:"expression"`
		} `json:"rules"`
	} `json:"properties"`
}

func upgradeResources(t *testing.T, raw json.RawMessage) []upgradeMonitoringResource {
	t.Helper()
	var resources []upgradeMonitoringResource
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		var symbolic map[string]upgradeMonitoringResource
		if err := json.Unmarshal(raw, &symbolic); err != nil {
			t.Fatal(err)
		}
		for _, resource := range symbolic {
			resources = append(resources, resource)
		}
	} else if err := json.Unmarshal(raw, &resources); err != nil {
		t.Fatal(err)
	}
	return resources
}

func upgradeParameterValue(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var parameter struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &parameter); err != nil || len(parameter.Value) == 0 {
		t.Fatalf("expected a compiled parameter value, got %s: %v", raw, err)
	}
	return parameter.Value
}

func upgradeString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("expected string expression, got %s: %v", raw, err)
	}
	return value
}

func upgradeEvaluationEnabled(t *testing.T, raw json.RawMessage, mode string) bool {
	t.Helper()
	expression := upgradeString(t, upgradeParameterValue(t, raw))
	switch expression {
	case "[not(equals(parameters('clusterUpgradeMonitorMode'), 'active'))]":
		return mode != "active"
	case "[equals(parameters('clusterUpgradeMonitorMode'), 'shadow')]":
		return mode == "shadow"
	case "[equals(parameters('clusterUpgradeMonitorMode'), 'active')]":
		return mode == "active"
	default:
		t.Fatalf("unexpected compiled evaluation expression %q", expression)
		return false
	}
}

func upgradeDeliveryCount(t *testing.T, raw json.RawMessage, mode string) int {
	t.Helper()
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		expression := upgradeString(t, raw)
		want := "[if(equals(parameters('clusterUpgradeMonitorMode'), 'active'), createObject('value', parameters('actionGroups')), createObject('value', createArray()))]"
		if expression != want {
			t.Fatalf("unexpected production delivery binding %q", expression)
		}
		if mode == "active" {
			return 2
		}
		return 0
	}
	value := upgradeParameterValue(t, raw)
	if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
		var groups []string
		if err := json.Unmarshal(value, &groups); err != nil || len(groups) != 0 {
			t.Fatalf("shadow binding must be an empty action-group array, got %s", value)
		}
		return 0
	}
	if expression := upgradeString(t, value); expression != "[parameters('actionGroups')]" {
		t.Fatalf("existing delivery binding changed: %q", expression)
	}
	return 2
}

func TestClusterUpgradeMonitoringModes(t *testing.T) {
	var template upgradeMonitoringTemplate
	compileIdentityTemplate(t, "modules/metrics/rp-rules.bicep", &template)
	modeParameter := template.Parameters["clusterUpgradeMonitorMode"]
	if upgradeString(t, modeParameter.DefaultValue) != "legacy" {
		t.Fatal("new cluster-upgrade paging must not be the default")
	}
	if len(modeParameter.AllowedValues) != 3 {
		t.Fatalf("expected exactly three rollout modes, got %v", modeParameter.AllowedValues)
	}
	resources := map[string]upgradeMonitoringResource{}
	for _, resource := range upgradeResources(t, template.Resources) {
		resources[resource.Name] = resource
	}
	for _, test := range []struct {
		mode                       string
		legacy, shadow, production bool
	}{
		{"legacy", true, false, false},
		{"shadow", true, true, false},
		{"active", false, false, true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			if !slices.Contains(modeParameter.AllowedValues, test.mode) {
				t.Fatalf("compiled template does not allow rollout mode %s", test.mode)
			}
			for _, detector := range []struct {
				name        string
				wantEnabled bool
				wantGroups  int
			}{
				{"legacyClusterUpgradeAlerts", test.legacy, 2},
				{"shadowClusterUpgradeAlerts", test.shadow, 0},
				{"clusterUpgradeAlerts", test.production, 0},
			} {
				if detector.name == "clusterUpgradeAlerts" && test.production {
					detector.wantGroups = 2
				}
				resource, found := resources[detector.name]
				if !found {
					t.Fatalf("missing detector module %s", detector.name)
				}
				if got := upgradeEvaluationEnabled(t, resource.Properties.Parameters["alertRulesEnabled"], test.mode); got != detector.wantEnabled {
					t.Errorf("%s evaluation: got %v, want %v", detector.name, got, detector.wantEnabled)
				}
				if got := upgradeDeliveryCount(t, resource.Properties.Parameters["actionGroups"], test.mode); got != detector.wantGroups {
					t.Errorf("%s delivery groups: got %d, want %d", detector.name, got, detector.wantGroups)
				}
			}
			if got := upgradeDeliveryCount(t, resources["generatedRPPrometheusAlertingRules"].Properties.Parameters["actionGroups"], test.mode); got != 2 {
				t.Fatal("rollout mode must not change other RP alert delivery")
			}
		})
	}
	groupNames := map[string]bool{}
	for name, wantName := range map[string]string{
		"legacyClusterUpgradeAlerts": "rp-user-journey-cluster-upgrade-monitor-rules",
		"shadowClusterUpgradeAlerts": "shadow-rp-user-journey-cluster-upgrade-monitor-rules-v2",
		"clusterUpgradeAlerts":       "rp-user-journey-cluster-upgrade-monitor-rules-v2",
	} {
		var child upgradeMonitoringTemplate
		if err := json.Unmarshal(resources[name].Properties.Template, &child); err != nil {
			t.Fatal(err)
		}
		groups := upgradeResources(t, child.Resources)
		if len(groups) != 1 || groups[0].Name != wantName || groupNames[wantName] {
			t.Fatalf("%s must have its unique expected group identity %s", name, wantName)
		}
		groupNames[wantName] = true
		wantRules := 1
		if name == "legacyClusterUpgradeAlerts" {
			wantRules = 2
		}
		if len(groups[0].Properties.Rules) != wantRules {
			t.Fatalf("%s has %d rules, want %d", name, len(groups[0].Properties.Rules), wantRules)
		}
		for _, rule := range groups[0].Properties.Rules {
			if upgradeString(t, rule.Enabled) != "[parameters('alertRulesEnabled')]" {
				t.Fatalf("%s rule %s ignores the evaluation switch", name, rule.Alert)
			}
			if name != "legacyClusterUpgradeAlerts" && rule.Expression != "hosted_control_plane_upgrade:stuck:bool == 1" {
				t.Fatalf("%s does not consume the shared cluster monitor", name)
			}
		}
		var core upgradeMonitoringTemplate
		if err := json.Unmarshal(resources["generatedRPPrometheusAlertingRules"].Properties.Template, &core); err != nil {
			t.Fatal(err)
		}
		for _, group := range upgradeResources(t, core.Resources) {
			if groupNames[group.Name] {
				t.Fatalf("upgrade group %s is also deployed by the shared RP module", group.Name)
			}
		}
	}
}

func TestClusterUpgradeMonitoringPipelineBinding(t *testing.T) {
	var template upgradeMonitoringTemplate
	compileIdentityTemplate(t, "templates/monitoring.bicep", &template)
	if upgradeString(t, template.Parameters["clusterUpgradeMonitorMode"].DefaultValue) != "legacy" {
		t.Fatal("monitoring pipeline default must preserve legacy paging")
	}
	for _, resource := range upgradeResources(t, template.Resources) {
		if resource.Name == "rpAlerts" {
			binding := upgradeString(t, upgradeParameterValue(t, resource.Properties.Parameters["clusterUpgradeMonitorMode"]))
			if binding != "[parameters('clusterUpgradeMonitorMode')]" {
				t.Fatalf("rollout mode is not forwarded to the RP wrapper: %s", binding)
			}
			return
		}
	}
	t.Fatal("missing RP alert module in monitoring template")
}
