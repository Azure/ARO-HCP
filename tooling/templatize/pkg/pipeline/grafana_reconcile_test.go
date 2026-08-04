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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/manage"
)

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
