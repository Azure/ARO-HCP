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
	"github.com/Azure/ARO-Tools/tools/stackctl/cmd/deletestack"
)

// runArmStackDeleteStep removes the deployment stack named by an ARMStack step with operation 'delete' from the
// step's resource group. It runs the shared stackctl code, so every runner of the step behaves the same way.
func runArmStackDeleteStep(ctx context.Context, id graph.Identifier, step *types.ARMStackStep, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	state.RLock()
	outputs := state.GetOutputs(id.Stamp)
	state.RUnlock()

	opts, err := armStackDeleteOptions(step, options.Configuration, outputs, id.ServiceGroup, options.Cloud, executionTarget.GetSubscriptionID(), executionTarget.GetResourceGroup())
	if err != nil {
		return err
	}
	return opts.Run(ctx)
}

func armStackDeleteOptions(step *types.ARMStackStep, cfg configtypes.Configuration, outputs Outputs, serviceGroup, cloud, subscriptionID, resourceGroup string) (*deletestack.RawOptions, error) {
	if step.DeploymentStackNameForDelete == nil {
		return nil, fmt.Errorf("deploymentStackNameForDelete is required with operation %q", types.ARMStackOperationDelete)
	}
	name, err := resolveValue(*step.DeploymentStackNameForDelete, cfg, outputs, serviceGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve deploymentStackNameForDelete: %w", err)
	}

	opts := deletestack.DefaultOptions()
	opts.CloudOptions.Cloud = cloud
	opts.SubscriptionID = subscriptionID
	opts.ResourceGroup = resourceGroup
	opts.Name = name
	opts.ActionOnUnmanage = step.ActionOnUnmanage

	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timeout %q: %w", step.Timeout, err)
		}
		opts.Timeout = d
	}

	return opts, nil
}
