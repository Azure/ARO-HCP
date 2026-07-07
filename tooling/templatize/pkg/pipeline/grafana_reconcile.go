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

func runGrafanaManageStep(id graph.Identifier, step *types.GrafanaManageStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	state.RLock()
	outputs := state.Outputs
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

	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return fmt.Errorf("failed to parse timeout %q: %w", step.Timeout, err)
		}
		opts.Timeout = d
	}

	return opts.Run(ctx)
}
