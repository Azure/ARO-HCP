package controllerutils

import (
	"context"
	"net/http"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MarkBillingDocumentDeleted patches a Cosmos DB document in the Billing container to add a deletion timestamp.
func MarkBillingDocumentDeleted(ctx context.Context, cosmosClient database.DBClient, clusterResourceID *azcorearm.ResourceID, deletionTime time.Time) error {
	logger := utils.LoggerFromContext(ctx)

	var patchOperations database.BillingDocumentPatchOperations
	patchOperations.SetDeletionTime(deletionTime)
	err := cosmosClient.PatchBillingDoc(ctx, clusterResourceID, patchOperations)
	if err == nil {
		logger.Info("Updated billing for cluster deletion")
	} else if database.IsResponseError(err, http.StatusNotFound) {
		// Log the error but proceed with normal processing.
		logger.Info("No billing document found")
		err = nil
	}

	return err
}

func DeleteRecursively(ctx context.Context, cosmosClient database.DBClient, rootResourceID *azcorearm.ResourceID) error {
	// now delete everything related to this item.  Operations will be cleaned up when ttl expires.
	// this does not do any advanced cleanup of content.  As we migrate more to cosmos, this will become more and more
	// stale.  Feel free to refactor if we can do a better job of cleanup at some point.
	untypedClient, err := cosmosClient.UntypedCRUD(*rootResourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	nestedContentIterator, err := untypedClient.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, nestedContent := range nestedContentIterator.Items(ctx) {
		nestedResourceID, err := api.CosmosIDToResourceID(nestedContent.ID)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := untypedClient.Delete(ctx, nestedResourceID); err != nil {
			return utils.TrackError(err)
		}
	}
	if err := untypedClient.Delete(ctx, rootResourceID); err != nil {
		return utils.TrackError(err)
	}

	return nil
}
