// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package externalauthdeletion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthDeletionController issues a Cosmos external auth delete
// for ExternalAuths that have their DeletionTimestamp and
// ClusterServiceDeletionTimestamp set and their ClusterServiceID
// cleared, once all child resources have been cleaned up.
type externalAuthDeletionController struct {
	externalAuthLister listers.ExternalAuthLister
	resourcesDBClient  database.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthDeletionController)(nil)

func NewExternalAuthDeletionController(
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthDeletionController{
		externalAuthLister: externalAuthLister,
		resourcesDBClient:  resourcesDBClient,
	}

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthDeletionController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether the deleter has unfinished business for the
// given ExternalAuth. All the following conditions must be met:
// - DeletionTimestamp must be set
// - ClusterServiceDeletionTimestamp must be set
// - ClusterServiceID must be nil
func (c *externalAuthDeletionController) NeedsWork(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	// TODO temporary check to skip the new deletion approach for ExternalAuths that were created before the new approach was implemented.
	// This will be removed once all externalauths whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !externalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach {
		return false
	}

	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID == nil
}

// SyncOnce calls Cosmos to delete the ExternalAuth when the NeedsWork
// condition is met and all the delete preconditions are met.
func (c *externalAuthDeletionController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) (controllerutil.SyncResult, error) {
	logger := utils.LoggerFromContext(ctx)

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if !c.NeedsWork(cachedExternalAuth) {
		return controllerutil.SyncResult{}, nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	externalAuth, err := externalAuthCRUD.Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}
	if !c.NeedsWork(externalAuth) {
		return controllerutil.SyncResult{}, nil
	}

	preconditionMet, err := c.deletePreconditionCosmosChildResourcesDeleted(ctx, key)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return controllerutil.SyncResult{}, nil
	}

	logger.Info("deleting external auth from Cosmos")
	err = externalAuthCRUD.Delete(ctx, key.HCPExternalAuthName)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to delete external auth from Cosmos: %w", err))
	}
	logger.Info("external auth deleted from Cosmos")

	return controllerutil.SyncResult{}, nil
}

// deletePreconditionCosmosChildResourcesDeleted checks if the cosmos
// child resources have been deleted. It ignores external auth
// controllers, as there might be controllers still running for the
// ExternalAuth until the very end of the deletion process.
func (c *externalAuthDeletionController) deletePreconditionCosmosChildResourcesDeleted(ctx context.Context, key controllerutils.HCPExternalAuthKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	externalAuthResourceID := key.GetResourceID()
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*externalAuthResourceID)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to create untyped CRUD for child check: %w", err))
	}
	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		// We ignore external auth controllers here, as there might be controllers still running for the ExternalAuth until the very
		// end of the deletion process
		if strings.EqualFold(childResource.ResourceType, api.ExternalAuthControllerResourceType.String()) {
			continue
		}
		logger.Info("child resource still exists, waiting for cleanup", "childResourceID", childResource.ResourceID)
		return false, nil
	}
	if err := childIterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("error iterating child resources: %w", err))
	}

	return true, nil
}
