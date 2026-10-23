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

package deletion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthClusterServiceIDClearer clears ClusterServiceID after the
// cluster-service ExternalAuth itself has been confirmed gone. This runs
// after the delete dispatch controller has already issued the delete
// request (ClusterServiceDeletionTimestamp is set). We poll
// cluster-service for the ExternalAuth and zero out the stored
// ClusterServiceID only when CS returns 404. Ready or uninstalling means
// Cluster Service still owns the ExternalAuth (and the IdP may still be
// active); we wait. Stuck deletes are failed by
// DeleteOperationCompletionDeadline on OperationExternalAuthDelete rather
// than by clearing the ID while CS still reports Ready.
type externalAuthClusterServiceIDClearer struct {
	externalAuthLister   corelisters.ExternalAuthLister
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceIDClearer)(nil)

const ExternalAuthDeletionClusterServiceIDClearerControllerName = "ExternalAuthDeletionClusterServiceIDClearer"

func NewExternalAuthClusterServiceIDClearerController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthClusterServiceIDClearer{
		externalAuthLister:   externalAuthLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewExternalAuthWatchingController(
		ExternalAuthDeletionClusterServiceIDClearerControllerName,
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ExternalAuthResourceType, "*")

	return controller
}

// NeedsWork reports whether this controller has unfinished business for the
// given ExternalAuth: deletion has been started (DeletionTimestamp), dispatch
// has completed (ClusterServiceDeletionTimestamp), and a ClusterServiceID is
// still recorded that needs verification before clearing.
func (c *externalAuthClusterServiceIDClearer) NeedsWork(externalAuth *coreapi.ExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID != nil && len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) > 0
}

// SyncOnce reads the ExternalAuth from cluster-service. ClusterServiceID is
// cleared only when cluster-service reports 404. Ready means teardown has
// not been observed yet (including the brief Ready window after an accepted
// DELETE). Uninstalling and any other in-progress state also wait.
func (c *externalAuthClusterServiceIDClearer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
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
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}
	if !c.NeedsWork(externalAuth) {
		return nil
	}

	csID := externalAuth.ServiceProviderProperties.ClusterServiceID
	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(csCtx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service ExternalAuth: %w", err))
		}
		// 404 - cluster-service has finished deleting the ExternalAuth, clear the CS ID.
		logger.Info("cluster-service ExternalAuth gone. Clearing ClusterServiceID", "clusterServiceID", csID.String())
		return c.clearClusterServiceID(ctx, externalAuthCRUD, externalAuth)
	}

	if clusterServiceExternalAuthIsReady(csExternalAuth) {
		// Accepted DELETE is not completion. CS usually leaves Ready briefly
		// before uninstalling then 404. A persistent Ready is a stuck delete;
		// OperationExternalAuthDelete fails it via DeleteOperationCompletionDeadline.
		logger.Info("cluster-service ExternalAuth still Ready after delete dispatch. Waiting for 404",
			"clusterServiceID", csID.String(),
			"clusterServiceDeletionTimestamp", externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time)
		return nil
	}

	// ExternalAuth still exists in cluster-service in a non-Ready state
	// (typically uninstalling). Nothing to do yet.
	return nil
}

func (c *externalAuthClusterServiceIDClearer) clearClusterServiceID(ctx context.Context, externalAuthCRUD corecosmosstorage.ExternalAuthsCRUD, externalAuth *coreapi.ExternalAuth) error {
	replacement := externalAuth.DeepCopy()
	replacement.ServiceProviderProperties.ClusterServiceID = nil
	_, err := externalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to clear ClusterServiceID: %w", err))
	}
	return nil
}

// clusterServiceExternalAuthIsReady reports whether Cluster Service still
// considers the ExternalAuth Ready. Ready is not deletion completion.
func clusterServiceExternalAuthIsReady(csExternalAuth *arohcpv1alpha1.ExternalAuth) bool {
	if csExternalAuth == nil {
		return false
	}
	return csExternalAuth.Status().State().Value() == string(operationbase.ExternalAuthStateReady)
}
