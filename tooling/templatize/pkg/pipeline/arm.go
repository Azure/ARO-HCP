package pipeline

import (
	"context"
	"fmt"
	"strings"

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

func (a *armClient) runArmStep(ctx context.Context, options *PipelineRunOptions, rgName string, step *ARMStep, input map[string]output) (output, error) {
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
func doDryRun(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *ARMStep, vars config.Variables, input map[string]output) (output, error) {

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
		return nil, fmt.Errorf("failed to create WhatIf Deployment: %w", err)
	}
	logger.Info("WhatIf Deployment started", "deployment", step.Name)

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	logger.Info("WhatIf Deployment finished successfully", "deployment", step.Name)

	fmt.Println("Change report for WhatIf deployment")
	fmt.Println("----------")
	fmt.Println("Creating")
	printChanges(armresources.ChangeTypeCreate, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("Deploy")
	printChanges(armresources.ChangeTypeDeploy, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("Modify")
	printChanges(armresources.ChangeTypeModify, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("Delete")
	printChanges(armresources.ChangeTypeDelete, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("Ignoring")
	printChanges(armresources.ChangeTypeIgnore, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("NoChange")
	printChanges(armresources.ChangeTypeNoChange, resp.Properties.Changes)
	fmt.Println("----------")
	fmt.Println("Unsupported")
	printChanges(armresources.ChangeTypeUnsupported, resp.Properties.Changes)

	return nil, nil
}

func doWaitForDeployment(ctx context.Context, client *armresources.DeploymentsClient, rgName string, step *ARMStep, vars config.Variables, input map[string]output) (output, error) {
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
