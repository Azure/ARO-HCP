package pipeline

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-logr/logr"
)

type armClient struct {
	creds          *azidentity.DefaultAzureCredential
	SubscriptionID string
	Region         string
}

func newArmClient(subscriptionID, region string) *armClient {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil
	}
	return &armClient{
		creds:          cred,
		SubscriptionID: subscriptionID,
		Region:         region,
	}
}

func (a *armClient) runArmStep(ctx context.Context, options *PipelineRunOptions, rgName string, step *Step, input map[string]output) (output, error) {
	// Ensure resourcegroup exists
	err := a.ensureResourceGroupExists(ctx, rgName, options.NoPersist)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	// Run deployment
	client, err := armresources.NewDeploymentsClient(a.SubscriptionID, a.creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployments client: %w", err)
	}
	if options.DryRun {
		return doDryRun(ctx, client, rgName, step, options.Vars, input)
	}

	return doWaitForDeployment(ctx, client, rgName, step, options.Vars, input)
}

func recursivePrint(logger Logger, change *armresources.WhatIfPropertyChange) {
	logger.Info(change.PropertyChangeType, change.Path)
	for _, child := range change.Children {
		recursivePrint(logger, child)
	}

}

func doDryRun(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *Step, vars config.Variables, input map[string]output) (output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	inputValues, err := getInputValues(step.Variables, input)
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

	poller, err := client.BeginWhatIf(ctx, rgName, step.Name, deployment, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create whatif deployment: %w", err)
	}
	logger.Info("Whatif Deployment started", "deployment", step.Name)

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	logger.Info("Whatif Deployment finished successfully", "deployment", step.Name)

	for _, change := range resp.Properties.Changes {
		logger.Info("%s %s", change.ChangeType, *change.ResourceID)
		for _, delta := range change.Delta {
			recursivePrint(logger, delta)
		}
	}

	return nil, nil
}

func doWaitForDeployment(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *Step, vars config.Variables, input map[string]output) (output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	inputValues, err := getInputValues(step.Variables, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARMDeployment(ctx, step.Parameters, vars, inputValues)
	if err != nil {
		return nil, fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	// Create the deployment
	deployment := armresources.Deployment{
		Properties: deploymentProperties,
	}

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, step.Name, deployment, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}
	logger.Info("Deployment started", "deployment", step.Name)

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	logger.Info("Deployment finished successfully", "deployment", step.Name, "responseId", *resp.ID)

	if resp.Properties.Outputs != nil {
		if outputMap, ok := resp.Properties.Outputs.(map[string]any); ok {
			returnMap := armOutput{}
			for k, v := range outputMap {
				returnMap[k] = v
			}
			return returnMap, nil
		}
	}
	return nil, nil
}

func (a *armClient) ensureResourceGroupExists(ctx context.Context, rgName string, rgNoPersist bool) error {
	// Create a new ARM client
	client, err := armresources.NewResourceGroupsClient(a.SubscriptionID, a.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create ARM client: %w", err)
	}

	// Check if the resource group exists
	// todo fill tags properly
	tags := map[string]*string{}

	if !rgNoPersist {
		// if no-persist is set, don't set the persist tag, needs double negotiate, cause default should be true
		tags["persist"] = to.Ptr("true")
	}
	_, err = client.Get(ctx, rgName, nil)
	if err != nil {
		// Create the resource group
		resourceGroup := armresources.ResourceGroup{
			Location: to.Ptr(a.Region),
			Tags:     tags,
		}
		_, err = client.CreateOrUpdate(ctx, rgName, resourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to create resource group: %w", err)
		}
	} else {
		patchResourceGroup := armresources.ResourceGroupPatchable{
			Tags: tags,
		}
		_, err = client.Update(ctx, rgName, patchResourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to update resource group: %w", err)
		}
	}
	return nil
}
