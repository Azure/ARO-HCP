package databasemutationhelpers

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type OperationAccessor interface {
	CompleteOperation(ctx context.Context, resourceIDString string) error
}

type operationAccessor struct {
	dbClient database.DBClient
}

func newOperationAccessor(dbClient database.DBClient) *operationAccessor {
	return &operationAccessor{dbClient: dbClient}
}

var _ OperationAccessor = &operationAccessor{}

func (c operationAccessor) CompleteOperation(ctx context.Context, resourceIDString string) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	if err := integrationutils.MarkOperationsCompleteForName(ctx, c.dbClient, resourceID.SubscriptionID, resourceID.Name); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
