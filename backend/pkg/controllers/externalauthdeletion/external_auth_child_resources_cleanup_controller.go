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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthChildResourcesCleanupController deletes child resources
// scoped under an ExternalAuth recursively once the ExternalAuth is
// marked for deletion and Cluster Service has confirmed the delete on
// its side. Controller status documents
// (ExternalAuthControllerResourceType) are left alone. The orphan
// scraper handles those after the ExternalAuth document itself is
// removed.
type externalAuthChildResourcesCleanupController struct {
	cooldownChecker    controllerutil.CooldownChecker
	externalAuthLister listers.ExternalAuthLister
	resourcesDBClient  database.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthChildResourcesCleanupController)(nil)

func NewExternalAuthChildResourcesCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthChildResourcesCleanupController{
		cooldownChecker:    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		externalAuthLister: externalAuthLister,
		resourcesDBClient:  resourcesDBClient,
	}

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthChildResourcesCleanupController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *externalAuthChildResourcesCleanupController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *externalAuthChildResourcesCleanupController) NeedsWork(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
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

func (c *externalAuthChildResourcesCleanupController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if !c.NeedsWork(cachedExternalAuth) {
		return nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	externalAuth, err := externalAuthCRUD.Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}
	if !c.NeedsWork(externalAuth) {
		return nil
	}

	externalAuthResourceID := key.GetResourceID()
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*externalAuthResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create untyped CRUD for external auth children: %w", err))
	}

	// extraDeleteGates determines which child resource types should be skipped during cleanup.
	// We never delete external auth controllers here, as there might be controllers still
	// running for the ExternalAuth until the very end of the deletion process.
	extraDeleteGates := map[string]func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error){
		strings.ToLower(api.ExternalAuthControllerResourceType.String()): func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error) { return false, nil },
	}

	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list external auth child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		if childResource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("child resource at cosmosID %q has no resourceID; refusing to delete", childResource.ID))
		}

		extraDeleteGate, ok := extraDeleteGates[strings.ToLower(childResource.ResourceType)]
		if ok {
			shouldDelete, err := extraDeleteGate(ctx, childResource.ResourceID)
			if err != nil {
				return utils.TrackError(err)
			}
			if !shouldDelete {
				continue
			}
		}

		logger.Info("deleting child resource", "childResourceID", childResource.ResourceID)
		if err := untypedCRUD.Delete(ctx, childResource.ResourceID); err != nil {
			return utils.TrackError(err)
		}
	}
	if err := childIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	return nil
}
