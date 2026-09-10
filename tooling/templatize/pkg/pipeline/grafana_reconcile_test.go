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

package pipeline

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/manage"
)

func TestDefaultReconcileOptionsPublicNetworkAccess(t *testing.T) {
	opts := manage.DefaultReconcileOptions()
	if opts.PublicNetworkAccess != "Enabled" {
		t.Fatalf("expected default PublicNetworkAccess 'Enabled' (backward compatible), got %q", opts.PublicNetworkAccess)
	}
}

func TestResolvePublicNetworkAccess(t *testing.T) {
	tests := []struct {
		name     string
		cfg      configtypes.Configuration
		step     types.Value
		expected string
	}{
		{
			name: "config ref resolves to Enabled",
			cfg: configtypes.Configuration{
				"monitoring": map[string]any{
					"grafanaPublicNetworkAccess": "Enabled",
				},
			},
			step:     types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"},
			expected: "Enabled",
		},
		{
			name: "config ref resolves to Disabled",
			cfg: configtypes.Configuration{
				"monitoring": map[string]any{
					"grafanaPublicNetworkAccess": "Disabled",
				},
			},
			step:     types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"},
			expected: "Disabled",
		},
		{
			name:     "empty value returns empty string (uses tool default)",
			cfg:      configtypes.Configuration{},
			step:     types.Value{},
			expected: "",
		},
		{
			name:     "direct value override",
			cfg:      configtypes.Configuration{},
			step:     types.Value{Value: "Disabled"},
			expected: "Disabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOptionalValue(tt.step, tt.cfg, Outputs{}, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("resolveOptionalValue() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// testID is a graph.Identifier shared by the buildGrafanaReconcileOptions tests below.
var testID = graph.Identifier{
	ServiceGroup: "Microsoft.Azure.ARO.HCP.Grafana",
	StepDependency: types.StepDependency{
		ResourceGroup: "singleton",
		Step:          "grafana-reconcile",
	},
}

// testExecutionTarget is a minimal, hand-populated ExecutionTarget used to verify that
// buildGrafanaReconcileOptions wires subscription/resource-group values through correctly.
var testExecutionTarget = &executionTargetImpl{
	subscriptionID: "11111111-1111-1111-1111-111111111111",
	resourceGroup:  "global",
}

// baseGrafanaManageStep returns a GrafanaManageStep with the fields that are always
// required (GrafanaName, Location) pre-populated with literal values, leaving
// PublicNetworkAccess unset so each test case can set it explicitly.
func baseGrafanaManageStep() *types.GrafanaManageStep {
	return &types.GrafanaManageStep{
		GrafanaName: types.Value{Value: "arohcp-dev"},
		Location:    types.Value{Value: "eastus"},
	}
}

// TestBuildGrafanaReconcileOptions exercises buildGrafanaReconcileOptions directly —
// the actual production wiring inside runGrafanaManageStep — rather than
// re-implementing its resolve-then-assign logic inline. This is what catches a
// regression like the PublicNetworkAccess resolution block being deleted or
// wired to the wrong step field, since that block runs for real here.
func TestBuildGrafanaReconcileOptions(t *testing.T) {
	t.Run("PublicNetworkAccess Disabled propagates from config", func(t *testing.T) {
		step := baseGrafanaManageStep()
		step.PublicNetworkAccess = types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"}
		cfg := configtypes.Configuration{
			"monitoring": map[string]any{
				"grafanaPublicNetworkAccess": "Disabled",
			},
		}

		opts, err := buildGrafanaReconcileOptions(testID, step, cfg, Outputs{}, testExecutionTarget)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.PublicNetworkAccess != "Disabled" {
			t.Errorf("PublicNetworkAccess = %q, want %q", opts.PublicNetworkAccess, "Disabled")
		}
	})

	t.Run("PublicNetworkAccess Enabled propagates from config", func(t *testing.T) {
		step := baseGrafanaManageStep()
		step.PublicNetworkAccess = types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"}
		cfg := configtypes.Configuration{
			"monitoring": map[string]any{
				"grafanaPublicNetworkAccess": "Enabled",
			},
		}

		opts, err := buildGrafanaReconcileOptions(testID, step, cfg, Outputs{}, testExecutionTarget)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.PublicNetworkAccess != "Enabled" {
			t.Errorf("PublicNetworkAccess = %q, want %q", opts.PublicNetworkAccess, "Enabled")
		}
	})

	t.Run("unset PublicNetworkAccess keeps the tool default", func(t *testing.T) {
		step := baseGrafanaManageStep() // PublicNetworkAccess left as zero-value types.Value{}

		opts, err := buildGrafanaReconcileOptions(testID, step, configtypes.Configuration{}, Outputs{}, testExecutionTarget)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.PublicNetworkAccess != "Enabled" {
			t.Errorf("PublicNetworkAccess = %q, want tool default %q", opts.PublicNetworkAccess, "Enabled")
		}
	})

	t.Run("resolution error on PublicNetworkAccess is propagated", func(t *testing.T) {
		step := baseGrafanaManageStep()
		step.PublicNetworkAccess = types.Value{ConfigRef: "monitoring.doesNotExist"}

		_, err := buildGrafanaReconcileOptions(testID, step, configtypes.Configuration{}, Outputs{}, testExecutionTarget)
		if err == nil {
			t.Fatal("expected an error for an unresolvable configRef, got nil")
		}
		if !strings.Contains(err.Error(), "publicNetworkAccess") {
			t.Errorf("expected error to mention publicNetworkAccess, got: %v", err)
		}
	})

	t.Run("other fields are wired through alongside PublicNetworkAccess", func(t *testing.T) {
		const crossTenantSecurityGroup = "11111111-1111-1111-1111-111111111111;22222222-2222-2222-2222-222222222222"
		step := baseGrafanaManageStep()
		step.PublicNetworkAccess = types.Value{Value: "Disabled"}
		step.CrossTenantSecurityGroup = types.Value{Value: crossTenantSecurityGroup}

		opts, err := buildGrafanaReconcileOptions(testID, step, configtypes.Configuration{}, Outputs{}, testExecutionTarget)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.GrafanaName != "arohcp-dev" {
			t.Errorf("GrafanaName = %q, want %q", opts.GrafanaName, "arohcp-dev")
		}
		if opts.SubscriptionID != testExecutionTarget.subscriptionID {
			t.Errorf("SubscriptionID = %q, want %q", opts.SubscriptionID, testExecutionTarget.subscriptionID)
		}
		if opts.ResourceGroup != testExecutionTarget.resourceGroup {
			t.Errorf("ResourceGroup = %q, want %q", opts.ResourceGroup, testExecutionTarget.resourceGroup)
		}
		if opts.CrossTenantSecurityGroup != crossTenantSecurityGroup {
			t.Errorf("CrossTenantSecurityGroup = %q, want %q", opts.CrossTenantSecurityGroup, crossTenantSecurityGroup)
		}
		if opts.PublicNetworkAccess != "Disabled" {
			t.Errorf("PublicNetworkAccess = %q, want %q", opts.PublicNetworkAccess, "Disabled")
		}
	})

	t.Run("ADX options wired through from step.ADX", func(t *testing.T) {
		step := baseGrafanaManageStep()
		step.ADX = &types.GrafanaADXIntegrations{
			Enabled:          types.Value{ConfigRef: "monitoring.enabled"},
			Environment:      types.Value{ConfigRef: "monitoring.environment"},
			Geographies:      types.Value{ConfigRef: "monitoring.geographies"},
			Scenario:         types.Value{ConfigRef: "monitoring.scenario"},
			TargetResourceID: types.Value{ConfigRef: "monitoring.targetResourceId"},
		}
		cfg := configtypes.Configuration{
			"monitoring": map[string]any{
				"enabled":          true,
				"environment":      " int ",
				"geographies":      " UK, eus2 ",
				"scenario":         " AzureDataExplorer ",
				"targetResourceId": " /subscriptions/example/targets/one ",
			},
		}

		opts, err := buildGrafanaReconcileOptions(testID, step, cfg, Outputs{}, testExecutionTarget)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !opts.ADXIntegrationsEnabled {
			t.Errorf("ADXIntegrationsEnabled = %v, want true", opts.ADXIntegrationsEnabled)
		}
		if opts.ADXEnvironment != "int" {
			t.Errorf("ADXEnvironment = %q, want %q", opts.ADXEnvironment, "int")
		}
		if opts.ADXGeographies != "UK, eus2" {
			t.Errorf("ADXGeographies = %q, want %q", opts.ADXGeographies, "UK, eus2")
		}
		if opts.ADXScenario != "AzureDataExplorer" {
			t.Errorf("ADXScenario = %q, want %q", opts.ADXScenario, "AzureDataExplorer")
		}
		if opts.ADXTargetResourceID != "/subscriptions/example/targets/one" {
			t.Errorf("ADXTargetResourceID = %q, want %q", opts.ADXTargetResourceID, "/subscriptions/example/targets/one")
		}
	})
}

func TestApplyGrafanaADXOptions(t *testing.T) {
	cfg := configtypes.Configuration{
		"monitoring": map[string]any{
			"enabled":          true,
			"environment":      " int ",
			"geographies":      " UK, eus2 ",
			"scenario":         " AzureDataExplorer ",
			"targetResourceId": " /subscriptions/example/targets/one ",
		},
	}
	adx := &types.GrafanaADXIntegrations{
		Enabled:          types.Value{ConfigRef: "monitoring.enabled"},
		Environment:      types.Value{ConfigRef: "monitoring.environment"},
		Geographies:      types.Value{ConfigRef: "monitoring.geographies"},
		Scenario:         types.Value{ConfigRef: "monitoring.scenario"},
		TargetResourceID: types.Value{ConfigRef: "monitoring.targetResourceId"},
	}
	opts := manage.DefaultReconcileOptions()

	err := applyGrafanaADXOptions(opts, adx, cfg, Outputs{}, "Microsoft.Azure.ARO.HCP.Global")
	require.NoError(t, err)
	assert.True(t, opts.ADXIntegrationsEnabled)
	assert.Equal(t, "int", opts.ADXEnvironment)
	assert.Equal(t, "UK, eus2", opts.ADXGeographies)
	assert.Equal(t, "AzureDataExplorer", opts.ADXScenario)
	assert.Equal(t, "/subscriptions/example/targets/one", opts.ADXTargetResourceID)
}

func TestApplyGrafanaADXOptionsWithoutADXConfiguration(t *testing.T) {
	opts := manage.DefaultReconcileOptions()

	err := applyGrafanaADXOptions(opts, nil, configtypes.Configuration{}, Outputs{}, "Microsoft.Azure.ARO.HCP.Global")
	require.NoError(t, err)
	assert.False(t, opts.ADXIntegrationsEnabled)
	assert.Empty(t, opts.ADXEnvironment)
	assert.Empty(t, opts.ADXGeographies)
}

func TestApplyGrafanaADXOptionsDisabled(t *testing.T) {
	adx := &types.GrafanaADXIntegrations{
		Enabled: types.Value{Value: false},
	}
	opts := manage.DefaultReconcileOptions()

	err := applyGrafanaADXOptions(opts, adx, configtypes.Configuration{}, Outputs{}, "Microsoft.Azure.ARO.HCP.Global")
	require.NoError(t, err)
	assert.False(t, opts.ADXIntegrationsEnabled)
}

func TestApplyGrafanaADXOptionsRejectsNonBooleanEnabled(t *testing.T) {
	cfg := configtypes.Configuration{
		"monitoring": map[string]any{
			"enabled": "not-a-bool",
		},
	}
	adx := &types.GrafanaADXIntegrations{
		Enabled: types.Value{ConfigRef: "monitoring.enabled"},
	}

	err := applyGrafanaADXOptions(manage.DefaultReconcileOptions(), adx, cfg, Outputs{}, "Microsoft.Azure.ARO.HCP.Global")
	assert.ErrorContains(t, err, "adx.enabled must resolve to a boolean")
}
