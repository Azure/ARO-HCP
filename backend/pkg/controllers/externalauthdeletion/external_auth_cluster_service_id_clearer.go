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
	"errors"
	"fmt"
	"net/http"
	"time"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthClusterServiceIDClearer clears ClusterServiceID after the
// cluster-service ExternalAuth itself has been confirmed gone. This runs
// after the delete dispatch controller has already issued the delete
// request (ClusterServiceDeletionTimestamp is set). We poll
// cluster-service for the ExternalAuth and, on 404, zero out the stored
// ClusterServiceID so downstream code knows the CS resource is fully gone.
type externalAuthClusterServiceIDClearer struct {
	externalAuthLister   listers.ExternalAuthLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceIDClearer)(nil)

func NewExternalAuthClusterServiceIDClearerController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthClusterServiceIDClearer{
		externalAuthLister:   externalAuthLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthDeletionClusterServiceIDClearer",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether this controller has unfinished business for the
// given ExternalAuth: deletion has been started (DeletionTimestamp), the deleter
// has already issued the CS delete (ClusterServiceDeletionTimestamp), and a
// ClusterServiceID is still recorded that needs verification before clearing.
func (c *externalAuthClusterServiceIDClearer) NeedsWork(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	// TODO temporary check to skip the new deletion approach for ExternalAuths that were created before the new approach was implemented.
	// This will be removed once all externalauths whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !externalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach {
		return false
	}

	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID != nil && len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) > 0
}

// SyncOnce reads the ExternalAuth from cluster-service. If
// cluster-service reports 404, the deletion has finished and we zero out
// ClusterServiceID. Any other state means cluster-service is still
// processing the deletion. We retry on the next sync.
func (c *externalAuthClusterServiceIDClearer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) (controllerutil.SyncResult, error) {
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

	csID := externalAuth.ServiceProviderProperties.ClusterServiceID
	_, err = c.clusterServiceClient.GetExternalAuth(ctx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get cluster-service ExternalAuth: %w", err))
		}
		// 404 - cluster-service has finished deleting the ExternalAuth, clear the CS ID.
		logger.Info("cluster-service ExternalAuth gone. Clearing ClusterServiceID", "clusterServiceID", csID.String())
		replacement := externalAuth.DeepCopy()
		replacement.ServiceProviderProperties.ClusterServiceID = nil
		if _, err := externalAuthCRUD.Replace(ctx, replacement, nil); err != nil {
			return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to clear ClusterServiceID: %w", err))
		}
		return controllerutil.SyncResult{}, nil
	}

	// ExternalAuth still exists in cluster-service. Nothing to do yet.
	return controllerutil.SyncResult{}, nil
}
