package pipeline

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func (s *step) runArmStep(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	// Transform Bicep to ARM
	deploymentProperties, err := transformBicepToARM(ctx, s.Parameters, options.Vars)
	if err != nil {
		return fmt.Errorf("failed to transform Bicep to ARM: %w", err)
	}

	// Create the deployment
	deploymentName := s.Name
	deployment := armresources.Deployment{
		Properties: deploymentProperties,
	}

	// Ensure resourcegroup exists
	err = s.ensureResourceGroupExists(ctx, executionTarget)
	if err != nil {
		return fmt.Errorf("failed to ensure resource group exists: %w", err)
	}

	// TODO handle dry-run

	// Run deployment
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to obtain a credential: %w", err)
	}

	client, err := armresources.NewDeploymentsClient(executionTarget.SubscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create deployments client: %w", err)
	}

	poller, err := client.BeginCreateOrUpdate(ctx, executionTarget.ResourceGroup, deploymentName, deployment, nil)
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	fmt.Println("Deployment started successfully")

	// Wait for completion
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	fmt.Printf("Deployment finished successfully: %s\n", *resp.ID)

	err = listDeploymentResources(ctx, executionTarget.SubscriptionID, executionTarget.ResourceGroup, deploymentName, cred)
	if err != nil {
		return fmt.Errorf("failed to list deployment resources: %w", err)
	}
	return nil
}

func (s *step) ensureResourceGroupExists(ctx context.Context, executionTarget *ExecutionTarget) error {
	// Create a new Azure identity client
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to obtain a credential: %w", err)
	}

	// Create a new ARM client
	client, err := armresources.NewResourceGroupsClient(executionTarget.SubscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create ARM client: %w", err)
	}

	// Check if the resource group exists
	// todo fill tags properly
	tags := map[string]*string{
		"persist": to.Ptr("true"),
	}
	_, err = client.Get(ctx, executionTarget.ResourceGroup, nil)
	if err != nil {
		// Create the resource group
		resourceGroup := armresources.ResourceGroup{
			Location: to.Ptr(executionTarget.Region),
			Tags:     tags,
		}
		_, err = client.CreateOrUpdate(ctx, executionTarget.ResourceGroup, resourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to create resource group: %w", err)
		}
	} else {
		patchResourceGroup := armresources.ResourceGroupPatchable{
			Tags: tags,
		}
		_, err = client.Update(ctx, executionTarget.ResourceGroup, patchResourceGroup, nil)
		if err != nil {
			return fmt.Errorf("failed to update resource group: %w", err)
		}
	}
	return nil
}

func listDeploymentResources(ctx context.Context, subscriptionID, resourceGroupName, deploymentName string, cred *azidentity.DefaultAzureCredential) error {
	// Create a new resources client
	resourcesClient, err := armresources.NewClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create resources client: %w", err)
	}

	// List resources of the deployment
	fmt.Printf("Resources for deployment %s:\n", deploymentName)
	pager := resourcesClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to get next page of resources: %w", err)
		}
		for _, resource := range page.Value {
			if resource.ManagedBy != nil && *resource.ManagedBy == deploymentName {
				fmt.Printf("- %s\n", *resource.ID)
				if *resource.Type == "Microsoft.Resources/deployments" {
					subDeploymentName := *resource.Name
					err := listDeploymentResources(ctx, subscriptionID, resourceGroupName, subDeploymentName, cred)
					if err != nil {
						return fmt.Errorf("failed to list resources for sub-deployment %s: %w", subDeploymentName, err)
					}
				}
			}
		}
	}

	return nil
}
