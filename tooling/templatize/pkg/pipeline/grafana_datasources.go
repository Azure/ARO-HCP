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
	"context"
	"fmt"
	"strconv"
	"strings"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/modify"
)

type resolvedGrafanaADXOptions struct {
	Enabled            bool
	DeleteWhenDisabled bool
	ClusterURL         string
	DefaultDatabase    string
	DatasourceName     string
	Geographies        string
	CurrentGeography   string
	DataConsistency    string
}

func runGrafanaDatasourcesStep(id graph.Identifier, step *types.GrafanaDatasourcesStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	opts := modify.DefaultAddDatasourceOptions()
	opts.GrafanaName = step.GrafanaName
	opts.SubscriptionID = executionTarget.GetSubscriptionID()
	opts.ResourceGroup = executionTarget.GetResourceGroup()

	if err := func() error {
		state.RLock()
		defer state.RUnlock()

		if step.GrafanaResourceID != nil {
			grafanaResourceID, ok, err := resolveGrafanaDatasourceValue(id.ServiceGroup, "grafanaResourceId", *step.GrafanaResourceID, options.Configuration, state.Outputs)
			if err != nil {
				return fmt.Errorf("failed to resolve grafanaResourceId: %w", err)
			}
			if ok {
				resolved, err := valueAsString("grafanaResourceId", grafanaResourceID)
				if err != nil {
					return err
				}
				opts.GrafanaResourceID = resolved
			}
		}

		if step.AzureMonitor != nil && step.AzureMonitor.Enabled != nil {
			opts.AzureMonitorEnabled = *step.AzureMonitor.Enabled
		}

		if step.ADX != nil {
			adx, err := resolveGrafanaADXOptions(id.ServiceGroup, step.ADX, options.Configuration, state.Outputs)
			if err != nil {
				return err
			}
			opts.ADXEnabled = adx.Enabled
			opts.ADXDeleteWhenDisabled = adx.DeleteWhenDisabled
			opts.ADXClusterURL = adx.ClusterURL
			opts.ADXDefaultDatabase = adx.DefaultDatabase
			opts.ADXDatasourceName = adx.DatasourceName
			opts.ADXGeographies = adx.Geographies
			opts.ADXCurrentGeography = adx.CurrentGeography
			opts.ADXDataConsistency = adx.DataConsistency
		}
		return nil
	}(); err != nil {
		return err
	}

	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if err := completed.Run(ctx); err != nil {
		return fmt.Errorf("grafana datasources step failed: %w", err)
	}

	return nil
}

func resolveGrafanaADXOptions(serviceGroup string, adx *types.GrafanaADXDatasource, cfg configtypes.Configuration, outputs Outputs) (*resolvedGrafanaADXOptions, error) {
	enabled, err := resolveOptionalBool(serviceGroup, "adx.enabled", adx.Enabled, cfg, outputs)
	if err != nil {
		return nil, err
	}
	geographies, err := resolveOptionalString(serviceGroup, "adx.geographies", adx.Geographies, cfg, outputs)
	if err != nil {
		return nil, err
	}
	currentGeography, err := resolveCurrentGeography(cfg)
	if err != nil {
		return nil, err
	}

	resolved := &resolvedGrafanaADXOptions{
		Enabled:            enabled,
		DeleteWhenDisabled: adx.DeleteWhenDisabled,
		Geographies:        geographies,
		CurrentGeography:   currentGeography,
		DataConsistency:    adx.DataConsistency,
	}

	resolved.DatasourceName, err = resolveOptionalString(serviceGroup, "adx.datasourceName", adx.DatasourceName, cfg, outputs)
	if err != nil {
		return nil, err
	}
	resolved.ClusterURL, err = resolveOptionalString(serviceGroup, "adx.clusterUrl", adx.ClusterURL, cfg, outputs)
	if err != nil {
		return nil, err
	}

	resolved.DefaultDatabase, err = resolveOptionalString(serviceGroup, "adx.defaultDatabase", adx.DefaultDatabase, cfg, outputs)
	if err != nil {
		return nil, err
	}

	return resolved, nil
}

func resolveOptionalBool(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (bool, error) {
	raw, ok, err := resolveGrafanaDatasourceValue(serviceGroup, name, value, cfg, outputs)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return valueAsBool(name, raw)
}

func resolveOptionalString(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (string, error) {
	raw, ok, err := resolveGrafanaDatasourceValue(serviceGroup, name, value, cfg, outputs)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return valueAsString(name, raw)
}

func resolveGrafanaDatasourceValue(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (any, bool, error) {
	if value.Input == nil && value.ConfigRef == "" && value.Value == nil {
		return nil, false, nil
	}

	values, err := getInputValues(serviceGroup, []types.Variable{{Name: name, Value: value}}, cfg, outputs)
	if err != nil {
		return nil, false, err
	}
	return values[name], true, nil
}

func resolveCurrentGeography(cfg configtypes.Configuration) (string, error) {
	currentRaw, err := cfg.GetByPath("azureGeoShortId")
	if err != nil {
		return "", fmt.Errorf("failed to lookup current geography short ID: %w", err)
	}
	currentGeography, err := valueAsString("azureGeoShortId", currentRaw)
	if err != nil {
		return "", err
	}
	return currentGeography, nil
}

func valueAsBool(name string, value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, fmt.Errorf("%s must resolve to a boolean, got %q", name, v)
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("%s must resolve to a boolean, got %T", name, value)
	}
}

func valueAsString(name string, value any) (string, error) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("%s must resolve to a string, got %T", name, value)
	}
}
