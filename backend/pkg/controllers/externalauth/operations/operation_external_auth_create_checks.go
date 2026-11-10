package operations

import (
	"context"
	"fmt"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type externalAuthCreateClusterServiceCreateCheck struct {
	externalAuthLister corelisters.ExternalAuthLister
}

var _ operationbase.OperationStatusCalculator[*externalAuthOperationCreateStatusInput] = (*externalAuthCreateClusterServiceCreateCheck)(nil)

func (c *externalAuthCreateClusterServiceCreateCheck) GetSourceName() string {
	return "externalAuthClusterServiceCreate"
}

func (c *externalAuthCreateClusterServiceCreateCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input *externalAuthOperationCreateStatusInput) (*operationbase.OperationState, error) {
	externalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}

	if externalAuth.ServiceProviderProperties.ClusterServiceID == nil || len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "Waiting for Cluster Service external auth creation"), nil
	}

	_, err = input.ClusterServiceExternalAuthOnceGetter(ctx, externalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil && ocm.IsOCMErrorStatus404(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster service external auth is not available"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil

}
