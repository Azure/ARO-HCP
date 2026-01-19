package controllers

import (
	"context"
	"fmt"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AzureHCPClusterResourceGroupExistenceValidation validates that the Azure Resource
// Group part of the HCP Cluster Resource ID being created exists beforehand.
type AzureHCPClusterResourceGroupExistenceValidation struct {
	azureFPAClientBuilder azureclient.FPAClientBuilder
}

func NewAzureHCPClusterResourceGroupExistenceValidation(
	azureFPAClientBuilder azureclient.FPAClientBuilder,
) *AzureHCPClusterResourceGroupExistenceValidation {
	return &AzureHCPClusterResourceGroupExistenceValidation{
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

func (a *AzureHCPClusterResourceGroupExistenceValidation) Name() string {
	return "azure-cluster-resource-group-existence-validation"
}

func (a *AzureHCPClusterResourceGroupExistenceValidation) Validate(
	ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster,
) error {
	rgClient, err := a.azureFPAClientBuilder.ResourceGroupsClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource groups client: %w", err))
	}

	_, err = rgClient.Get(ctx, cluster.ID.ResourceGroupName, nil)
	if azureclient.IsResourceGroupNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("resource group does not exist: %w", err))
	}

	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource group: %w", err))
	}

	return nil
}
