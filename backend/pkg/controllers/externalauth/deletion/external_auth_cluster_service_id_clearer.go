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

	utilsclock "k8s.io/utils/clock"

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

// readyExternalAuthDeletionGracePeriod is how long a Ready Cluster Service
// ExternalAuth may remain after delete dispatch before that Ready is treated
// as stuck. DeleteExternalAuth only means Cluster Service accepted the
// request. A healthy delete leaves Ready for uninstalling and then 404, and
// the informer event from stamping ClusterServiceDeletionTimestamp can
// observe the initial Ready. Clearing ClusterServiceID on that observation
// lets Cosmos delete the document while Cluster Service still owns the
// ExternalAuth. OperationExternalAuthDelete waits on Ready for the same
// reason. After this grace, a persistent Ready is the stuck-delete case.
const readyExternalAuthDeletionGracePeriod = 2 * time.Minute

// externalAuthClusterServiceIDClearer clears ClusterServiceID after the
// cluster-service ExternalAuth itself has been confirmed gone. This runs
// after the delete dispatch controller has already issued the delete
// request (ClusterServiceDeletionTimestamp is set). We poll
// cluster-service for the ExternalAuth and zero out the stored
// ClusterServiceID when CS returns 404, or when it still returns Ready
// after readyExternalAuthDeletionGracePeriod from dispatch (CS does not
// transition some ExternalAuths to 404, which otherwise deadlocks deletion).
// Ready is not treated as terminal when dispatch recorded that DELETE was
// not accepted. Documents dispatched before that flag existed (nil) use the
// same grace, then Ready-clear.
type externalAuthClusterServiceIDClearer struct {
	clock                utilsclock.PassiveClock
	externalAuthLister   corelisters.ExternalAuthLister
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceIDClearer)(nil)

const ExternalAuthDeletionClusterServiceIDClearerControllerName = "ExternalAuthDeletionClusterServiceIDClearer"

func NewExternalAuthClusterServiceIDClearerController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthClusterServiceIDClearer{
		clock:                clock,
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
// cleared when cluster-service reports 404, or when it still reports Ready
// after readyExternalAuthDeletionGracePeriod from dispatch and DELETE was
// accepted (or the accepted flag is unset on a pre-field document). Ready
// inside that grace, or after an explicit non-accepted dispatch
// (parent-cluster uninstalling 400), waits. Uninstalling (and any other
// in-progress state) means cluster-service is still processing; we retry on
// the next sync.
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
		if clusterServiceExternalAuthDeleteExplicitlyRejected(externalAuth) {
			// Dispatch stamped ClusterServiceDeletionTimestamp because the parent
			// cluster is uninstalling (400); CS never accepted DELETE for this
			// ExternalAuth. Wait for 404 rather than dropping the ID while CS
			// still owns the object. A nil flag is not rejection: those are
			// in-flight documents dispatched before the field existed.
			logger.Info("cluster-service ExternalAuth still Ready, DELETE was not accepted. Waiting for 404",
				"clusterServiceID", csID.String())
			return nil
		}
		if !c.readyExternalAuthDeletionGraceElapsed(externalAuth) {
			// Accepted DELETE (or a pre-field dispatch) is not completion. CS
			// usually leaves Ready for uninstalling before 404. The informer
			// event from stamping the dispatch timestamp observes this Ready.
			logger.Info("cluster-service ExternalAuth still Ready after delete dispatch. Waiting for async deletion to leave Ready",
				"clusterServiceID", csID.String(),
				"clusterServiceDeletionTimestamp", externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time)
			return nil
		}
		// CS stayed Ready for the full grace and never 404s. Clearing the ID
		// unblocks ExternalAuthDeletionController so the ARM operation can
		// complete instead of waiting forever for a Cosmos delete that cannot
		// happen while ClusterServiceID is still set.
		logger.Info("cluster-service ExternalAuth still Ready after delete grace. Clearing ClusterServiceID",
			"clusterServiceID", csID.String())
		return c.clearClusterServiceID(ctx, externalAuthCRUD, externalAuth)
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

// clusterServiceExternalAuthIsReady reports the Cluster Service state. Ready is
// both the normal state before an accepted DELETE transitions to uninstalling
// and the state Cluster Service keeps when a delete never finishes. A single
// Ready observation is not completion; callers apply readyExternalAuthDeletionGracePeriod.
func clusterServiceExternalAuthIsReady(csExternalAuth *arohcpv1alpha1.ExternalAuth) bool {
	if csExternalAuth == nil {
		return false
	}
	return csExternalAuth.Status().State().Value() == string(operationbase.ExternalAuthStateReady)
}

// readyExternalAuthDeletionGraceElapsed reports whether dispatch is old enough
// that a still-Ready ExternalAuth is the stuck-delete case rather than an
// asynchronous delete that has not left Ready yet.
func (c *externalAuthClusterServiceIDClearer) readyExternalAuthDeletionGraceElapsed(externalAuth *coreapi.ExternalAuth) bool {
	dispatchedAt := externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp
	if dispatchedAt == nil {
		return false
	}
	return c.clock.Since(dispatchedAt.Time) >= readyExternalAuthDeletionGracePeriod
}

// clusterServiceExternalAuthDeleteExplicitlyRejected reports that dispatch
// recorded Cluster Service did not accept DELETE. A nil flag is left unset on
// documents stamped before this field existed; those must not wait for 404.
func clusterServiceExternalAuthDeleteExplicitlyRejected(externalAuth *coreapi.ExternalAuth) bool {
	accepted := externalAuth.ServiceProviderProperties.ClusterServiceExternalAuthDeleteAccepted
	return accepted != nil && !*accepted
}
