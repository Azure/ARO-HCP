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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

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

// missingClusterServiceIDTimeout is how long we wait after first observing
// DeletionTimestamp for the ClusterServiceID to appear before concluding
// that the corresponding Cluster Service ExternalAuth was never created and we
// have no work to do (or before treating a 404 from Cluster Service as definitive).
const missingClusterServiceIDTimeout = 120 * time.Second

// externalAuthClusterServiceDeleteDispatchSyncer issues a Cluster Service
// delete for any ExternalAuth whose DeletionTimestamp has been set. The
// frontend records the timestamp on the ExternalAuth when
// DeleteExternalAuth is invoked, this controller picks it up and calls
// Cluster Service out-of-band so the frontend never has to block on it.
// Once the controller has issued the delete (or given up waiting for a
// ClusterServiceID), it stamps ClusterServiceDeletionTimestamp on the
// ExternalAuth to record that this step is complete and avoid re-issuing
// the delete on subsequent syncs.
type externalAuthClusterServiceDeleteDispatchSyncer struct {
	clock                utilsclock.PassiveClock
	cooldownChecker      controllerutil.CooldownChecker
	externalAuthLister   listers.ExternalAuthLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	// firstSeenDeletionTimestampCache contains the time the controller
	// first saw the serviceProviderProperties.deletionTimestamp being set
	// for an external auth. The cache key is the lowercased external
	// auth's resource ID and the value is a time.Time in UTC.
	firstSeenDeletionTimestampCache *lru.Cache
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceDeleteDispatchSyncer)(nil)

func NewExternalAuthClusterServiceDeleteDispatchController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthClusterServiceDeleteDispatchSyncer{
		clock:                           clock,
		cooldownChecker:                 controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		externalAuthLister:              externalAuthLister,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		firstSeenDeletionTimestampCache: lru.New(50000),
	}

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthClusterServiceDeleteDispatch",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *externalAuthClusterServiceDeleteDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the deleter has unfinished business for the given
// ExternalAuth: DeletionTimestamp must be set and ClusterServiceDeletionTimestamp
// must not yet be set.
func (c *externalAuthClusterServiceDeleteDispatchSyncer) NeedsWork(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	// TODO temporary check to skip the new deletion approach for ExternalAuths that were created before the new approach was implemented.
	// This will be removed once all externalauths whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !externalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach {
		return false
	}

	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil
}

// SyncOnce calls Cluster Service to delete the ExternalAuth when its
// DeletionTimestamp is set.
//
// If the ExternalAuth has no ClusterServiceID yet, we may have raced
// cluster-service ExternalAuth creation. We wait for
// missingClusterServiceIDTimeout from when we first observed
// DeletionTimestamp before concluding the cluster-service ExternalAuth
// was never created.
//
// In either terminal case -- CS delete issued or wait abandoned -- we
// stamp ClusterServiceDeletionTimestamp so the next sync short-circuits.
func (c *externalAuthClusterServiceDeleteDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
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

	externalAuthDeletionTimestamp := externalAuth.ServiceProviderProperties.DeletionTimestamp.Time
	cacheKey := strings.ToLower(externalAuth.ID.String())
	var firstSeenExternalAuthDeletionTimestamp time.Time
	firstSeenEntry, ok := c.firstSeenDeletionTimestampCache.Get(cacheKey)
	if ok {
		firstSeenExternalAuthDeletionTimestamp = firstSeenEntry.(time.Time)
	} else {
		firstSeenExternalAuthDeletionTimestamp = c.clock.Now().UTC()
		c.firstSeenDeletionTimestampCache.Add(cacheKey, firstSeenExternalAuthDeletionTimestamp)
	}

	csID := externalAuth.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		elapsed := c.clock.Since(firstSeenExternalAuthDeletionTimestamp)
		if elapsed < missingClusterServiceIDTimeout {
			// The frontend may still be in the middle of creating the cluster-service
			// ExternalAuth, or the controller that does so hasn't run yet. Re-check on the
			// next sync. The resync interval and informer change events drive retries.
			return nil
		}
		logger.Info("giving up on cluster-service ExternalAuth delete - ClusterServiceID never appeared",
			"externalAuthDeletionTimestamp", externalAuthDeletionTimestamp, "externalAuthFirstSeenDeletionTimestamp", firstSeenExternalAuthDeletionTimestamp)
	} else if err := c.clusterServiceClient.DeleteExternalAuth(ctx, *csID); err != nil {
		var ocmError *ocmerrors.Error

		switch {
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Cannot delete ExternalAuth: its parent cluster must be in deletable state") &&
			strings.Contains(ocmError.Reason(), "Parent cluster state: 'uninstalling'"):
			// If the error is indicating that the parent cluster is already being
			// uninstalled we consider that the external auth is already being deleted
			// because Cluster Service on cluster deletion will end up deleting the
			// externalauths as well.
			// Matching an error message is brittle, but Clusters Service
			// returns 400 Bad Request for a wide range of errors and there
			// is no other information in the response to distinguish them.
			logger.Info("ExternalAuth already being deleted by cluster-service via parent cluster deletion", "clusterServiceID", csID.String())
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound:
			// OCM error 404 - could be a stale CSID or a race against an in-flight CS
			// create. Wait before treating the ExternalAuth as definitively gone.
			elapsed := c.clock.Since(firstSeenExternalAuthDeletionTimestamp)
			if elapsed < missingClusterServiceIDTimeout {
				return nil
			}
			logger.Info("cluster-service ExternalAuth already deleted or race against in-flight CS create", "clusterServiceID", csID.String())
		default:
			return utils.TrackError(fmt.Errorf("failed to delete cluster-service ExternalAuth: %w", err))
		}
	} else {
		logger.Info("requested cluster-service ExternalAuth delete", "clusterServiceID", csID.String())
	}

	externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: c.clock.Now().UTC()}
	_, err = externalAuthCRUD.Replace(ctx, externalAuth, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to stamp ClusterServiceDeletionTimestamp: %w", err))
	}
	c.firstSeenDeletionTimestampCache.Remove(cacheKey)

	return nil
}
