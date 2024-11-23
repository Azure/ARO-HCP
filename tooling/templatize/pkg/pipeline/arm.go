package pipeline

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-logr/logr"
)

func (s *Step) runArmStep(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	logger := logr.FromContextOrDiscard(ctx)

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
	logger.Info("Deployment started", "deployment", deploymentName)

	// Wait for completion
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for deployment completion: %w", err)
	}
	logger.Info("Deployment finished successfully", "deployment", deploymentName, "responseId", *resp.ID)
	return nil
}

func (s *Step) ensureResourceGroupExists(ctx context.Context, executionTarget *ExecutionTarget) error {
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
