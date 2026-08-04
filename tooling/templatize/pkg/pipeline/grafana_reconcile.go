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
	"time"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/manage"
)

func resolveOptionalValue(v types.Value, cfg configtypes.Configuration, outputs Outputs, serviceGroup string) (string, error) {
	if v.Value == nil && v.ConfigRef == "" && v.Input == nil {
		return "", nil
	}
	return resolveValue(v, cfg, outputs, serviceGroup)
}

func resolveGrafanaManageOptionalValue(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (any, bool, error) {
	if value.Input == nil && value.ConfigRef == "" && value.Value == nil {
		return nil, false, nil
	}

	values, err := getInputValues(serviceGroup, []types.Variable{{Name: name, Value: value}}, cfg, outputs)
	if err != nil {
		return nil, false, err
	}
	return values[name], true, nil
}

func resolveGrafanaManageOptionalBool(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (bool, error) {
	raw, ok, err := resolveGrafanaManageOptionalValue(serviceGroup, name, value, cfg, outputs)
	if err != nil || !ok {
		return false, err
	}

	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, fmt.Errorf("%s must resolve to a boolean, got %q", name, v)
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("%s must resolve to a boolean, got %T", name, raw)
	}
}

func resolveGrafanaManageOptionalString(serviceGroup, name string, value types.Value, cfg configtypes.Configuration, outputs Outputs) (string, error) {
	raw, ok, err := resolveGrafanaManageOptionalValue(serviceGroup, name, value, cfg, outputs)
	if err != nil || !ok {
		return "", err
	}

	resolved, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must resolve to a string, got %T", name, raw)
	}
	return strings.TrimSpace(resolved), nil
}

func applyGrafanaADXOptions(opts *manage.RawReconcileOptions, adx *types.GrafanaADXIntegrations, cfg configtypes.Configuration, outputs Outputs, serviceGroup string) error {
	if adx == nil {
		return nil
	}

	enabled, err := resolveGrafanaManageOptionalBool(serviceGroup, "adx.enabled", adx.Enabled, cfg, outputs)
	if err != nil {
		return fmt.Errorf("failed to resolve adx.enabled: %w", err)
	}
	environment, err := resolveGrafanaManageOptionalString(serviceGroup, "adx.environment", adx.Environment, cfg, outputs)
	if err != nil {
		return fmt.Errorf("failed to resolve adx.environment: %w", err)
	}
	geographies, err := resolveGrafanaManageOptionalString(serviceGroup, "adx.geographies", adx.Geographies, cfg, outputs)
	if err != nil {
		return fmt.Errorf("failed to resolve adx.geographies: %w", err)
	}
	scenario, err := resolveGrafanaManageOptionalString(serviceGroup, "adx.scenario", adx.Scenario, cfg, outputs)
	if err != nil {
		return fmt.Errorf("failed to resolve adx.scenario: %w", err)
	}
	targetResourceID, err := resolveGrafanaManageOptionalString(serviceGroup, "adx.targetResourceId", adx.TargetResourceID, cfg, outputs)
	if err != nil {
		return fmt.Errorf("failed to resolve adx.targetResourceId: %w", err)
	}

	opts.ADXIntegrationsEnabled = enabled
	opts.ADXEnvironment = environment
	opts.ADXGeographies = geographies
	opts.ADXScenario = scenario
	opts.ADXTargetResourceID = targetResourceID
	return nil
}

func runGrafanaManageStep(id graph.Identifier, step *types.GrafanaManageStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	state.RLock()
	outputs := state.GetOutputs(id.Stamp)
	state.RUnlock()

	grafanaName, err := resolveValue(step.GrafanaName, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve grafanaName: %w", err)
	}
	location, err := resolveValue(step.Location, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve location: %w", err)
	}

	opts := manage.DefaultReconcileOptions()
	opts.GrafanaName = grafanaName
	opts.SubscriptionID = executionTarget.GetSubscriptionID()
	opts.ResourceGroup = executionTarget.GetResourceGroup()
	opts.Location = location

	sku, err := resolveOptionalValue(step.SKU, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve sku: %w", err)
	}
	if sku != "" {
		opts.SKU = sku
	}

	majorVersion, err := resolveOptionalValue(step.MajorVersion, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve majorVersion: %w", err)
	}
	opts.MajorVersion = majorVersion

	zoneRedundancy, err := resolveOptionalValue(step.ZoneRedundancy, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve zoneRedundancy: %w", err)
	}
	if zoneRedundancy != "" {
		opts.ZoneRedundancy = zoneRedundancy
	}

	crossTenantSecurityGroup, err := resolveOptionalValue(step.CrossTenantSecurityGroup, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve crossTenantSecurityGroup: %w", err)
	}
	opts.CrossTenantSecurityGroup = crossTenantSecurityGroup

	if err := applyGrafanaADXOptions(opts, step.ADX, options.Configuration, outputs, id.ServiceGroup); err != nil {
		return err
	}

	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return fmt.Errorf("failed to parse timeout %q: %w", step.Timeout, err)
		}
		opts.Timeout = d
	}

	return opts.Run(ctx)
}
