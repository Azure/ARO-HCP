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
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

type armClient struct {
	bicepClient *bicep.LSPClient

	deploymentClient        *armresources.DeploymentsClient
	operationsClient        *armresources.DeploymentOperationsClient
	resourceGroupClient     *armresources.ResourceGroupsClient
	deploymentRetryWaitTime int

	Region        string
	GetDeployment func(ctx context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error)
}

func newArmClient(subscriptionID, region string, bicepClient *bicep.LSPClient) (*armClient, error) {
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	deploymentClient, err := armresources.NewDeploymentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment client: %w", err)
	}
	operationsClient, err := armresources.NewDeploymentOperationsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment operations client: %w", err)
	}
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group client: %w", err)
	}
	return &armClient{
		bicepClient:             bicepClient,
		deploymentClient:        deploymentClient,
		operationsClient:        operationsClient,
		deploymentRetryWaitTime: 15,
		resourceGroupClient:     resourceGroupClient,
		Region:                  region,
		GetDeployment: func(ctx context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
			return deploymentClient.Get(ctx, rgName, deploymentName, nil)
		},
	}, nil
}

func (a *armClient) runArmStep(ctx context.Context, options *StepRunOptions, rgName string, id graph.Identifier, step *types.ARMStep, state *ExecutionState) (Output, DetailsProducer, error) {
	// Ensure resourcegroup exists
	err := ensureResourceGroupExists(ctx, a.resourceGroupClient, a.Region, rgName, !options.NoPersist)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	if !options.DryRun || (options.DryRun && step.OutputOnly) {
		return doWaitForDeployment(ctx, a.bicepClient, a.deploymentClient, a.operationsClient, id.ServiceGroup, rgName, step, options.PipelineDirectory, options.StepCacheDir, options.Configuration, options.DeploymentTimeoutSeconds, options.RetryAttempt, state)
	}

	return doDryRun(ctx, a.bicepClient, a.deploymentClient, id.ServiceGroup, rgName, step, options.PipelineDirectory, options.StepCacheDir, options.Configuration, state)
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

const charset = "abcdefghijklmnopqrstuvwxyz" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ" + "0123456789"

func randString() string {
	output := strings.Builder{}
	for i := 0; i < 32; i++ {
		output.WriteByte(charset[rand.Intn(len(charset))])
	}
	return output.String()
}

func doDryRun(ctx context.Context, bicepClient *bicep.LSPClient, client *armresources.DeploymentsClient, sgName, rgName string, step *types.ARMStep, pipelineWorkingDir, stepCacheDir string, cfg configtypes.Configuration, state *ExecutionState) (Output, DetailsProducer, error) {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	inputValues, err := getInputValues(sgName, step.Variables, cfg, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMWhatIfDeployment(ctx, bicepClient, step.Parameters, step.DeploymentMode, pipelineWorkingDir, cfg, inputValues)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	// Create the deployment
	deployment := armresources.DeploymentWhatIf{
		Properties: deploymentProperties,
	}

	inputs := whatIfInputs{
		Properties:      deploymentProperties,
		ResourceGroup:   rgName,
		DeploymentLevel: step.DeploymentLevel,
	}

	skip, commit, err := checkSentinel(logger, inputs, stepCacheDir)
	if err != nil {
		return nil, nil, err
	}
	if skip {
		return nil, nil, nil
	}

	deploymentName := randString()

	if step.DeploymentLevel == "Subscription" {
		// Hardcode until schema is adapted
		deployment.Location = to.Ptr("eastus2")
		poller, err := client.BeginWhatIfAtSubscriptionScope(ctx, deploymentName, deployment, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", deploymentName)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	} else {
		poller, err := client.BeginWhatIf(ctx, rgName, deploymentName, deployment, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", deploymentName)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	}

	return nil, nil, commit()
}

type whatIfInputs struct {
	Properties      *armresources.DeploymentWhatIfProperties
	ResourceGroup   string
	DeploymentLevel string
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

	return armOutputFromOutputs(outputs), nil
}

func armOutputFromOutputs(outputs any) ArmOutput {
	if outputs != nil {
		if outputMap, ok := outputs.(map[string]any); ok {
			returnMap := ArmOutput{}
			for k, v := range outputMap {
				returnMap[k] = v
			}
			return returnMap
		}
	}
	return nil
}

func doWaitForDeployment(ctx context.Context, bicepClient *bicep.LSPClient, client *armresources.DeploymentsClient, operationsClient *armresources.DeploymentOperationsClient, sgName, rgName string, step *types.ARMStep, pipelineWorkingDir, stepCacheDir string, cfg configtypes.Configuration, timeoutSeconds int, retryAttempt int, state *ExecutionState) (Output, DetailsProducer, error) {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	inputValues, err := getInputValues(sgName, step.Variables, cfg, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMDeployment(ctx, bicepClient, step.Parameters, step.DeploymentMode, pipelineWorkingDir, cfg, inputValues)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	if hasTemplateResources(deploymentProperties.Template) && step.OutputOnly {
		return nil, nil, fmt.Errorf("deployment step %s is outputOnly, but contains resources", step.Name)
	}

	// Create the deployment
	deployment := armresources.Deployment{
		Properties: deploymentProperties,
	}

	inputs := deploymentInputs{
		Properties:      deploymentProperties,
		ResourceGroup:   rgName,
		DeploymentLevel: step.DeploymentLevel,
		RetryAttempt:    retryAttempt,
	}

	digest, skip, commit, err := checkCachedOutput[ArmOutput](logger, inputs, stepCacheDir)
	if err != nil {
		return nil, nil, err
	}
	if skip != nil {
		return skip, nil, nil
	}

	// when there's no step cache, there's no digest
	deploymentName := randString()
	if digest != "" {
		deploymentName = digest
	}

	var output ArmOutput
	var details DetailsProducer
	exists, output, details, err := pollAndGetOutputFromExistingDeployment(ctx, client, operationsClient, timeoutSeconds, step.DeploymentLevel, rgName, deploymentName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to poll previously-existing deployment: %w", err)
	}
	if exists {
		return output, details, commit(output)
	}

	logger.V(2).Info("Starting ARM deployment")
	var pollErr error
	if step.DeploymentLevel == "Subscription" {
		details = DetermineOperationsForSubscriptionDeployment(operationsClient, deploymentName)
		// Hardcode until schema is adapted
		deployment.Location = to.Ptr("eastus2")
		poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, deploymentName, deployment, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.V(1).Info("Deployment started", "deployment", deploymentName)

		output, pollErr = pollAndGetOutput(ctx, poller)
	} else {
		details = DetermineOperationsForResourceGroupDeployment(operationsClient, rgName, deploymentName)
		poller, err := client.BeginCreateOrUpdate(ctx, rgName, deploymentName, deployment, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.V(1).Info("Deployment started", "deployment", deploymentName)

		output, pollErr = pollAndGetOutput(ctx, poller)
	}
	if pollErr != nil {
		return nil, nil, fmt.Errorf("failed to poll deployment: %w", pollErr)
	}

	return output, details, commit(output)
}

// pollAndGetOutputFromExistingDeployment papers over the unfortunate reality that armresources.DeploymentClient has
// no mechanism to create a poller for an existing deployment - relegating us to a manual polling loop.
func pollAndGetOutputFromExistingDeployment(ctx context.Context, client *armresources.DeploymentsClient, operationsClient *armresources.DeploymentOperationsClient, timeoutSeconds int, deploymentLevel, resourceGroup, deploymentName string) (bool, ArmOutput, DetailsProducer, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return false, nil, nil, err
	}
	logger = logger.WithValues("deployment", deploymentName)

	var details DetailsProducer
	var get func(ctx context.Context) (output *armresources.DeploymentPropertiesExtended, err error)
	if deploymentLevel == "Subscription" {
		details = DetermineOperationsForSubscriptionDeployment(operationsClient, deploymentName)
		get = func(ctx context.Context) (output *armresources.DeploymentPropertiesExtended, err error) {
			response, err := client.GetAtSubscriptionScope(ctx, deploymentName, nil)
			return response.Properties, err
		}
	} else {
		details = DetermineOperationsForResourceGroupDeployment(operationsClient, resourceGroup, deploymentName)
		get = func(ctx context.Context) (output *armresources.DeploymentPropertiesExtended, err error) {
			response, err := client.Get(ctx, resourceGroup, deploymentName, nil)
			return response.Properties, err
		}
	}

	logger.V(4).Info("Searching for pre-existing deployment")
	output, err := get(ctx)
	var azerror *azcore.ResponseError
	notFound := errors.As(err, &azerror) && azerror.StatusCode == http.StatusNotFound
	if notFound {
		return false, nil, nil, nil
	}

	logger.V(2).Info("Waiting for existing deployment to complete")
	remainingTime := timeoutSeconds
	for remainingTime > 0 {
		if err != nil {
			return false, nil, nil, fmt.Errorf("failed to look up pre-existing deployment: %w", err)
		}
		if output.ProvisioningState == nil {
			return false, nil, nil, fmt.Errorf("failed to look up pre-existing deployment: found a nil provisioning state")
		}
		switch *output.ProvisioningState {
		case armresources.ProvisioningStateCanceled, armresources.ProvisioningStateDeleted, armresources.ProvisioningStateDeleting, armresources.ProvisioningStateFailed, armresources.ProvisioningStateNotSpecified:
			if output.Error != nil {
				return false, nil, nil, fmt.Errorf("pre-existing deployment failed: %w", createError(*output.Error))
			}
			return false, nil, nil, fmt.Errorf("pre-existing deployment: provisioning state is %s", *output.ProvisioningState)
		case armresources.ProvisioningStateSucceeded:
			logger.V(2).Info("short-circuiting using results from previously-succeeded deployment")
			return true, armOutputFromOutputs(output.Outputs), details, nil
		case armresources.ProvisioningStateAccepted, armresources.ProvisioningStateCreated, armresources.ProvisioningStateCreating, armresources.ProvisioningStateReady, armresources.ProvisioningStateRunning, armresources.ProvisioningStateUpdating:
			// keep waiting for the deployment to finish
		}
		time.Sleep(15 * time.Second)
		remainingTime -= 15

		output, err = get(ctx)
	}
	return false, nil, nil, errors.New("timed out waiting for pre-existing deployment to finish")
}

type deploymentInputs struct {
	Properties      *armresources.DeploymentProperties
	ResourceGroup   string
	DeploymentLevel string
	RetryAttempt    int
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
