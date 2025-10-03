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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-logr/logr"
)

type armClient struct {
	deploymentClient        *armresources.DeploymentsClient
	resourceGroupClient     *armresources.ResourceGroupsClient
	deploymentRetryWaitTime int

	Region        string
	GetDeployment func(ctx context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error)
}

func newArmClient(subscriptionID, region string) (*armClient, error) {
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	deploymentClient, err := armresources.NewDeploymentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment client: %w", err)
	}
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group client: %w", err)
	}
	return &armClient{
		deploymentClient:        deploymentClient,
		deploymentRetryWaitTime: 15,
		resourceGroupClient:     resourceGroupClient,
		Region:                  region,
		GetDeployment: func(ctx context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
			return deploymentClient.Get(ctx, rgName, deploymentName, nil)
		},
	}, nil
}

// generateDeploymentName generates a unique deployment name for ARM steps.
// For outputOnly steps, it appends a random suffix to avoid conflicts when
// the same step runs multiple times concurrently or sequentially.
func generateDeploymentName(step *types.ARMStep) string {
	if step.OutputOnly {
		// Generate a random 8-character hex suffix for outputOnly steps
		suffix := make([]byte, 4)
		if _, err := rand.Read(suffix); err != nil {
			// Fallback to timestamp if random generation fails
			return fmt.Sprintf("%s-%d", step.Name, time.Now().Unix())
		}
		return fmt.Sprintf("%s-%s", step.Name, hex.EncodeToString(suffix))
	}
	return step.Name
}

func (a *armClient) getExistingDeployment(ctx context.Context, rgName, deploymentName string) (*armresources.DeploymentsClientGetResponse, error) {
	resp, err := a.GetDeployment(ctx, rgName, deploymentName)
	if err != nil && !strings.Contains(err.Error(), "ERROR CODE: DeploymentNotFound") {
		return nil, err
	}
	return &resp, nil
}

func (a *armClient) waitForExistingDeployment(ctx context.Context, timeOutInSeconds int, rgName, deploymentName string) error {
	for timeOutInSeconds > 0 {
		resp, err := a.getExistingDeployment(ctx, rgName, deploymentName)
		if err != nil {
			return fmt.Errorf("error getting deployment %w", err)
		}
		if resp.Properties == nil {
			return nil
		}
		if *resp.Properties.ProvisioningState != armresources.ProvisioningStateRunning {
			return nil
		}
		time.Sleep(time.Duration(a.deploymentRetryWaitTime) * time.Second)
		timeOutInSeconds -= a.deploymentRetryWaitTime
	}
	return fmt.Errorf("timeout exeeded waiting for deployment %s in rg %s", deploymentName, rgName)
}

func (a *armClient) runArmStep(ctx context.Context, options *StepRunOptions, rgName string, step *types.ARMStep, state *ExecutionState) (Output, error) {
	// Ensure resourcegroup exists
	err := ensureResourceGroupExists(ctx, a.resourceGroupClient, a.Region, rgName, !options.NoPersist)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	// Run deployment
	deploymentName := generateDeploymentName(step)

	if err := a.waitForExistingDeployment(ctx, options.DeploymentTimeoutSeconds, rgName, deploymentName); err != nil {
		return nil, fmt.Errorf("error waiting for deploymenty %w", err)
	}

	if !options.DryRun || (options.DryRun && step.OutputOnly) {
		return doWaitForDeployment(ctx, a.deploymentClient, rgName, deploymentName, step, options.PipelineDirectory, options.Configuration, state)
	}

	return doDryRun(ctx, a.deploymentClient, rgName, deploymentName, step, options.PipelineDirectory, options.Configuration, state)
}

func recursivePrint(level int, change *armresources.WhatIfPropertyChange) {
	fmt.Printf("%s%s:\n", strings.Repeat("\t", level), *change.Path)
	fmt.Printf("%s\tBefore:%s\n", strings.Repeat("\t", level), change.Before)
	fmt.Printf("%s\tAfter:%s\n", strings.Repeat("\t", level), change.After)
	for _, child := range change.Children {
		level += level
		recursivePrint(level, child)
	}
}

func printChanges(t armresources.ChangeType, changes []*armresources.WhatIfChange) {
	for _, change := range changes {
		if *change.ChangeType == t {
			fmt.Printf("%s %s\n", strings.Repeat("\t", 1), *change.ResourceID)
			for _, delta := range change.Delta {
				recursivePrint(2, delta)
			}
		}
	}
}

func printChangeReport(changes []*armresources.WhatIfChange) {
	fmt.Println("Change report for WhatIf deployment")
	fmt.Println("----------")
	fmt.Println("Creating")
	printChanges(armresources.ChangeTypeCreate, changes)
	fmt.Println("----------")
	fmt.Println("Deploy")
	printChanges(armresources.ChangeTypeDeploy, changes)
	fmt.Println("----------")
	fmt.Println("Modify")
	printChanges(armresources.ChangeTypeModify, changes)
	fmt.Println("----------")
	fmt.Println("Delete")
	printChanges(armresources.ChangeTypeDelete, changes)
	fmt.Println("----------")
	fmt.Println("Ignoring")
	printChanges(armresources.ChangeTypeIgnore, changes)
	fmt.Println("----------")
	fmt.Println("NoChange")
	printChanges(armresources.ChangeTypeNoChange, changes)
	fmt.Println("----------")
	fmt.Println("Unsupported")
	printChanges(armresources.ChangeTypeUnsupported, changes)
}

func createError(errors armresources.ErrorResponse) error {
	errB, err := errors.MarshalJSON()
	if err != nil {
		return err
	}
	return fmt.Errorf("%s", string(errB))
}

func pollAndPrint[T any](ctx context.Context, p *runtime.Poller[T]) error {
	resp, err := p.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	switch m := any(resp).(type) {
	case armresources.DeploymentsClientWhatIfResponse:
		if *m.Status == "Failed" {
			return createError(*m.Error)
		}
		printChangeReport(m.Properties.Changes)
	case armresources.DeploymentsClientWhatIfAtSubscriptionScopeResponse:
		if *m.Status == "Failed" {
			return createError(*m.Error)
		}
		printChangeReport(m.Properties.Changes)
	default:
		return fmt.Errorf("unknown type %T", m)
	}
	return nil
}

func doDryRun(ctx context.Context, client *armresources.DeploymentsClient, rgName string, deploymentName string, step *types.ARMStep, pipelineWorkingDir string, cfg config.Configuration, state *ExecutionState) (Output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	inputValues, err := getInputValues(step.Variables, cfg, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMWhatIfDeployment(ctx, step.Parameters, step.DeploymentMode, pipelineWorkingDir, cfg, inputValues)
	if err != nil {
		return nil, fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	// Create the deployment
	deployment := armresources.DeploymentWhatIf{
		Properties: deploymentProperties,
	}

	if step.DeploymentLevel == "Subscription" {
		// Hardcode until schema is adapted
		deployment.Location = to.Ptr("eastus2")
		poller, err := client.BeginWhatIfAtSubscriptionScope(ctx, deploymentName, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", deploymentName)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	} else {
		poller, err := client.BeginWhatIf(ctx, rgName, deploymentName, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", deploymentName)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	}

	return nil, nil
}

func pollAndGetOutput[T any](ctx context.Context, p *runtime.Poller[T]) (ArmOutput, error) {
	respRaw, err := p.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deployment completion: %w", err)
	}

	var outputs any

	switch resp := any(respRaw).(type) {
	case armresources.DeploymentsClientCreateOrUpdateResponse:
		outputs = resp.Properties.Outputs
	case armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse:
		outputs = resp.Properties.Outputs
	default:
		return nil, fmt.Errorf("unknown type %T", resp)
	}

	if outputs != nil {
		if outputMap, ok := outputs.(map[string]any); ok {
			returnMap := ArmOutput{}
			for k, v := range outputMap {
				returnMap[k] = v
			}
			return returnMap, nil
		}
	}
	return nil, nil
}

func doWaitForDeployment(ctx context.Context, client *armresources.DeploymentsClient, rgName string, deploymentName string, step *types.ARMStep, pipelineWorkingDir string, cfg config.Configuration, state *ExecutionState) (Output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	inputValues, err := getInputValues(step.Variables, cfg, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMDeployment(ctx, step.Parameters, step.DeploymentMode, pipelineWorkingDir, cfg, inputValues)
	if err != nil {
		return nil, fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	if hasTemplateResources(deploymentProperties.Template) && step.OutputOnly {
		return nil, fmt.Errorf("deployment step %s is outputOnly, but contains resources", step.Name)
	}

	// Create the deployment
	deployment := armresources.Deployment{
		Properties: deploymentProperties,
	}

	if step.DeploymentLevel == "Subscription" {
		// Hardcode until schema is adapted
		deployment.Location = to.Ptr("eastus2")
		poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, deploymentName, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.V(1).Info("Deployment started", "deployment", deploymentName)

		return pollAndGetOutput(ctx, poller)
	} else {
		poller, err := client.BeginCreateOrUpdate(ctx, rgName, deploymentName, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.V(1).Info("Deployment started", "deployment", deploymentName)

		return pollAndGetOutput(ctx, poller)
	}
}

// computeResourceGroupTags determines the final tags for a resource group based on existing tags and persist settings.
//
// Persist tag rules:
// 1. If persist tag already exists and is "true", it must be preserved (safety: never remove protection)
// 2. If persist is true, set persist tag to "true"
// 3. If persist is false and persist tag doesn't exist or isn't "true", don't add it
//
// This function is pure and easily testable - it only depends on its inputs.
func computeResourceGroupTags(existingTags map[string]*string, persist bool) map[string]*string {
	// Start with a copy of existing tags to avoid modifying the original
	var resultTags map[string]*string
	if existingTags != nil {
		resultTags = maps.Clone(existingTags)
	} else {
		resultTags = make(map[string]*string)
	}

	// Check current persist tag value
	currentPersistValue := ""
	if existingTags != nil && existingTags["persist"] != nil {
		currentPersistValue = *existingTags["persist"]
	}

	// Apply persist tag rules
	if currentPersistValue == "true" || persist {
		// Rule 1: Always preserve existing persist=true (critical safety rule)
		// Rule 2: Set persist=true when persist is true
		resultTags["persist"] = to.Ptr("true")
	} else {
		// Rule 3: Remove persist tag when persist is false and existing persist != "true"
		delete(resultTags, "persist")
	}
	return resultTags
}

func ensureResourceGroupExists(ctx context.Context, resourceGroupClient *armresources.ResourceGroupsClient, region, rgName string, persist bool) error {
	rg, err := resourceGroupClient.Get(ctx, rgName, nil)
	if err != nil {
		// Resource group doesn't exist - create it
		// We don't have any existing tags, so pass an empty map instead of nil for clarity.
		tags := computeResourceGroupTags(map[string]*string{}, persist)
		resourceGroup := armresources.ResourceGroup{
			Location: to.Ptr(region),
			Tags:     tags,
		}
		_, err = resourceGroupClient.CreateOrUpdate(ctx, rgName, resourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to create resource group: %w", err)
		}
	} else {
		// Resource group exists - update its tags
		tags := computeResourceGroupTags(rg.Tags, persist)
		patchResourceGroup := armresources.ResourceGroupPatchable{
			Tags: tags,
		}
		_, err = resourceGroupClient.Update(ctx, rgName, patchResourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to update resource group: %w", err)
		}
	}
	return nil
}
