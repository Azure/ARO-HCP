// Copyright 2025 Microsoft Corporation
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

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// runArmStackStep transforms a .bicep + .bicepparam into an ARM deployment stack, creates or updates the stack based on the
// step name, and waits for the stack to finish deploying. This logic is a transliteration of the equivalent logic in the `az` CLI:
// https://github.com/Azure/azure-cli/blob/cf11272c36d2680a65bd775e10d338afa3a8b902/src/azure-cli/azure/cli/command_modules/resource/custom.py#L1396-L1405
func runArmStackStep(
	ctx context.Context,
	options *StepRunOptions,
	executionTarget ExecutionTarget,
	id graph.Identifier,
	step *types.ARMStackStep,
	state *ExecutionState,
) (Output, DetailsProducer, error) {
	logger := logr.FromContextOrDiscard(ctx)

	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	resourceGroupClient, err := armresources.NewResourceGroupsClient(executionTarget.GetSubscriptionID(), cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource group client: %w", err)
	}
	stackClient, err := armdeploymentstacks.NewClient(executionTarget.GetSubscriptionID(), cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create deployment stack client: %w", err)
	}
	operationsClient, err := armresources.NewDeploymentOperationsClient(executionTarget.GetSubscriptionID(), cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create deployment operations client: %w", err)
	}

	if err := ensureResourceGroupExists(ctx, resourceGroupClient, executionTarget.GetRegion(), executionTarget.GetResourceGroup(), !options.NoPersist); err != nil {
		return nil, nil, fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	state.RLock()
	inputValues, err := getInputValues(id.ServiceGroup, step.Variables, options.Configuration, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get input values: %w", err)
	}

	template, params, err := transformParameters(ctx, options.BicepClient, options.Configuration, inputValues, step.Parameters, options.PipelineDirectory)
	if err != nil {
		return nil, nil, err
	}

	adaptedParams := map[string]*armdeploymentstacks.DeploymentParameter{}
	for k, v := range params {
		asMap, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("failed to convert parameter %s to map, got %T", k, v)
		}
		adaptedParams[k] = &armdeploymentstacks.DeploymentParameter{
			Value: asMap["value"],
		}
	}

	stack := armdeploymentstacks.DeploymentStack{
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				Resources:        ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnum(step.ActionOnUnmanage)),
				ResourceGroups:   ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnum(step.ActionOnUnmanage)),
				ManagementGroups: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnum(step.ActionOnUnmanage)),
			},
			BypassStackOutOfSyncError: ptr.To(step.BypassStackOutOfSyncError),
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode:               ptr.To(armdeploymentstacks.DenySettingsModeNone),
				ApplyToChildScopes: ptr.To(false),
				ExcludedActions:    []*string{},
				ExcludedPrincipals: []*string{},
			},
			Parameters: adaptedParams,
			Template:   template,
		},
	}

	inputs := stackInputs{
		Stack:           &stack,
		DeploymentLevel: step.DeploymentLevel,
		ResourceGroup:   executionTarget.GetResourceGroup(),
		StepName:        step.StepName(),
	}

	_, skip, commit, err := checkCachedOutput[ArmOutput](logger, inputs, options.StepCacheDir)
	if err != nil {
		return nil, nil, err
	}
	if skip != nil {
		return skip, nil, nil
	}

	var output armdeploymentstacks.DeploymentStack
	var details DetailsProducer
	switch step.DeploymentLevel {
	case "Subscription":
		details = DetermineOperationsForSubscriptionDeployment(operationsClient, step.StepName())
		stack.Location = ptr.To(executionTarget.GetRegion())
		poller, err := stackClient.BeginCreateOrUpdateAtSubscription(ctx, step.StepName(), stack, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create or update deployment stack at subscription scope: %w", err)
		}
		result, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, details, fmt.Errorf("failed to wait for deployment stack at subscription scope: %w", err)
		}
		output = result.DeploymentStack
	case "ResourceGroup":
		details = DetermineOperationsForResourceGroupDeployment(operationsClient, executionTarget.GetResourceGroup(), step.StepName())
		poller, err := stackClient.BeginCreateOrUpdateAtResourceGroup(ctx, executionTarget.GetResourceGroup(), step.StepName(), stack, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create or update deployment stack at resource group scope: %w", err)
		}
		result, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, details, fmt.Errorf("failed to wait for deployment stack at subscription scope: %w", err)
		}
		output = result.DeploymentStack
	default:
		return nil, nil, fmt.Errorf("invalid deployment level: %s", step.DeploymentLevel)
	}

	if output.Properties.Outputs != nil {
		if outputMap, ok := output.Properties.Outputs.(map[string]any); ok {
			returnMap := ArmOutput{}
			for k, v := range outputMap {
				returnMap[k] = v
			}
			return returnMap, details, commit(returnMap)
		}
	}
	return nil, details, nil
}

type stackInputs struct {
	Stack           *armdeploymentstacks.DeploymentStack
	DeploymentLevel string
	ResourceGroup   string
	StepName        string
}
