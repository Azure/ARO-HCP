package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"

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

func newArmClient(subscriptionID, region string) *armClient {
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return nil
	}
	deploymentClient, err := armresources.NewDeploymentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil
	}
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil
	}
	return &armClient{
		deploymentClient:        deploymentClient,
		deploymentRetryWaitTime: 15,
		resourceGroupClient:     resourceGroupClient,
		Region:                  region,
		GetDeployment: func(ctx context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
			return deploymentClient.Get(ctx, rgName, deploymentName, nil)
		},
	}
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

func (a *armClient) runArmStep(ctx context.Context, options *PipelineRunOptions, rgName string, step *ARMStep, input map[string]output) (output, error) {
	// Ensure resourcegroup exists
	err := a.ensureResourceGroupExists(ctx, rgName, options.NoPersist)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	// Run deployment

	if err := a.waitForExistingDeployment(ctx, options.DeploymentTimeoutSeconds, rgName, step.Name); err != nil {
		return nil, fmt.Errorf("error waiting for deploymenty %w", err)
	}

	if !options.DryRun || (options.DryRun && step.OutputOnly) {
		return doWaitForDeployment(ctx, a.deploymentClient, rgName, step, options.Vars, input)
	}

	return doDryRun(ctx, a.deploymentClient, rgName, step, options.Vars, input)
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

func doDryRun(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *ARMStep, vars config.Variables, input map[string]output) (output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	inputValues, err := getInputValues(step.Variables, vars, input, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMWhatIfDeployment(ctx, step.Parameters, vars, inputValues)
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
		poller, err := client.BeginWhatIfAtSubscriptionScope(ctx, step.Name, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", step.Name)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	} else {
		poller, err := client.BeginWhatIf(ctx, rgName, step.Name, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
		}
		logger.Info("WhatIf Deployment started", "deployment", step.Name)
		err = pollAndPrint(ctx, poller)
		if err != nil {
			return nil, fmt.Errorf("failed to poll and print: %w", err)
		}
	}

	return nil, nil
}

func pollAndGetOutput[T any](ctx context.Context, p *runtime.Poller[T]) (armOutput, error) {
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
			returnMap := armOutput{}
			for k, v := range outputMap {
				returnMap[k] = v
			}
			return returnMap, nil
		}
	}
	return nil, nil
}

func doWaitForDeployment(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *ARMStep, vars config.Variables, input map[string]output) (output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	inputValues, err := getInputValues(step.Variables, vars, input, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMDeployment(ctx, step.Parameters, vars, inputValues)
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
		poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, step.Name, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.Info("Deployment started", "deployment", step.Name)

		return pollAndGetOutput(ctx, poller)
	} else {
		poller, err := client.BeginCreateOrUpdate(ctx, rgName, step.Name, deployment, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		logger.Info("Deployment started", "deployment", step.Name)

		return pollAndGetOutput(ctx, poller)
	}
}

func (a *armClient) ensureResourceGroupExists(ctx context.Context, rgName string, rgNoPersist bool) error {
	// Check if the resource group exists
	// todo fill tags properly
	tags := map[string]*string{}

	if !rgNoPersist {
		// if no-persist is set, don't set the persist tag, needs double negotiate, cause default should be true
		tags["persist"] = to.Ptr("true")
	}
	_, err := a.resourceGroupClient.Get(ctx, rgName, nil)
	if err != nil {
		// Create the resource group
		resourceGroup := armresources.ResourceGroup{
			Location: to.Ptr(a.Region),
			Tags:     tags,
		}
		_, err = a.resourceGroupClient.CreateOrUpdate(ctx, rgName, resourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to create resource group: %w", err)
		}
	} else {
		patchResourceGroup := armresources.ResourceGroupPatchable{
			Tags: tags,
		}
		_, err = a.resourceGroupClient.Update(ctx, rgName, patchResourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to update resource group: %w", err)
		}
	}
	return nil
}
