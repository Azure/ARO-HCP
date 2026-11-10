package operations

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type externalAuthUpdateClusterServiceSpecCheck struct {
	externalAuthLister corelisters.ExternalAuthLister
}

var _ operationbase.OperationStatusCalculator[*externalAuthUpdateOperationStatusInput] = (*externalAuthUpdateClusterServiceSpecCheck)(nil)

func (c *externalAuthUpdateClusterServiceSpecCheck) GetSourceName() string {
	return "clusterServiceExternalAuthSpec"
}

// CalculateOperationStatus reports whether Cluster Service external auth spec fields
// match desired state intent for the external auth update operation. Only checks outside CS .status.
func (c *externalAuthUpdateClusterServiceSpecCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input *externalAuthUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	externalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if externalAuth.ServiceProviderProperties.ClusterServiceID == nil || len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "external auth cluster service ID is not set"), nil
	}
	csExternalAuth, err := input.ClusterServiceExternalAuthOnceGetter(ctx, externalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil && ocm.IsOCMErrorStatus404(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "cluster service external auth is not available"), nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster service external auth from cluster service: %w", err))
	}

	matches, message, err := c.clusterServiceExternalAuthSpecMatchesDesired(externalAuth, csExternalAuth)
	if err != nil {
		return nil, err
	}
	if !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type externalAuthUpdateHypershiftHostedClusterCheck struct {
	externalAuthLister corelisters.ExternalAuthLister
	readDesireLister   kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[*externalAuthUpdateOperationStatusInput] = (*externalAuthUpdateHypershiftHostedClusterCheck)(nil)

func (c *externalAuthUpdateHypershiftHostedClusterCheck) GetSourceName() string {
	return "hypershiftHostedClusterExternalAuth"
}

// CalculateOperationStatus contains the external auth update operation state calculation
// comparing desired state against Hypershift's HostedCluster in the management cluster.
func (c *externalAuthUpdateHypershiftHostedClusterCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input *externalAuthUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	externalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}

	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		externalAuth.ID.SubscriptionID,
		externalAuth.ID.ResourceGroupName,
		externalAuth.ID.Parent.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "Hypershift HostedCluster has not been observed yet"), nil
	}

	matches, message := c.hypershiftHostedClusterExternalAuthSpecMatchesDesired(externalAuth, hostedCluster)
	if !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}

	// TODO compare with Hypershift HostedCluster relevant parts of status.configuration.authentication when possible.
	// At the moment of writing this (2026-06-29) the status.configuration.authentication is only available on
	// HostedClusters >= 4.21.

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
