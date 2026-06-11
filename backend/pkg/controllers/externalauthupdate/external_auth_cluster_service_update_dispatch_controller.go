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

package externalauthupdate

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type externalAuthClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	externalAuthLister   listers.ExternalAuthLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceUpdateDispatchSyncer)(nil)

func NewExternalAuthClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	syncer := &externalAuthClusterServiceUpdateDispatchSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		externalAuthLister:   externalAuthLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthClusterServiceUpdateDispatch",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func externalAuthShouldProceed(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	if externalAuth.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	// TODO remove this check but keep the inner one when all external auths have been moved to the new update approach.
	// We guard it with this check because when the boolean is false we want to set the config hash in the
	// ServiceProviderExternalAuth independently on whether CSID is set or not.
	if externalAuth.ServiceProviderProperties.UsesNewExternalAuthUpdateApproach {
		csID := externalAuth.ServiceProviderProperties.ClusterServiceID
		if csID == nil || len(csID.String()) == 0 {
			return false
		}
	}

	return true
}

func (c *externalAuthClusterServiceUpdateDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *externalAuthClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if !externalAuthShouldProceed(cachedExternalAuth) {
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
	if !externalAuthShouldProceed(externalAuth) {
		return nil
	}

	serviceProviderExternalAuth, err := database.GetOrCreateServiceProviderExternalAuth(ctx, c.resourcesDBClient, externalAuth.ID)
	if err != nil {
		return err
	}

	// For the old update approach, we introduce this mechanism to set the config hash in the ServiceProviderExternalAuth status when the controller runs
	// so we can compute the hash for pre-existing external auths.
	if !externalAuth.ServiceProviderProperties.UsesNewExternalAuthUpdateApproach {
		desiredHash, err := ocm.ExternalAuthUpdatableConfigHash(externalAuth)
		if err != nil {
			return err
		}

		// TODO once we create the initial hash during extauth creation this shouldn't be needed
		storedHash := serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch
		storedVersionPtr := serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashVersionForUpdateDispatch
		currentVersion := ocm.ExternalAuthUpdatableConfigHashVersion
		storedVersion := currentVersion
		if storedVersionPtr != nil {
			storedVersion = *storedVersionPtr
		}

		if storedHash != desiredHash || storedVersion != currentVersion {
			logger.Info("using old update approach, skipping Cluster Service update but setting config hash", "desiredHash", desiredHash, "version", currentVersion)
			serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch = desiredHash
			serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashVersionForUpdateDispatch = &currentVersion
			_, err = c.resourcesDBClient.ServiceProviderExternalAuths(
				externalAuth.ID.SubscriptionID,
				externalAuth.ID.ResourceGroupName,
				externalAuth.ID.Parent.Name,
				externalAuth.ID.Name,
			).Replace(ctx, serviceProviderExternalAuth, nil)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderExternalAuth config hash: %w", err))
			}
			return nil
		}
		return nil
	}

	// If the hash or the hash version has not been calculated it means that the corresponding create controller hasn't
	// acted yet. Don't do until that occurs.
	if serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch == "" || serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashVersionForUpdateDispatch == nil {
		return nil
	}

	storedHash := serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch
	storedVersion := *serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashVersionForUpdateDispatch

	// Compare using the stored version so that a code deploy that changes the
	// field list (version bump) does not trigger unnecessary CS PATCHes. Only
	// the frontend advances the stored version on user-initiated updates.
	desiredHash, err := ocm.ExternalAuthUpdatableConfigHashForVersion(externalAuth, storedVersion)
	if err != nil {
		return err
	}
	if desiredHash == storedHash {
		return nil
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return err
	}

	externalAuthCSID := externalAuth.ServiceProviderProperties.ClusterServiceID
	logger.Info("dispatching external auth update to Cluster Service",
		"clusterServiceID", externalAuthCSID.String(),
		"previousHash", storedHash,
		"desiredHash", desiredHash,
		"version", storedVersion,
	)

	_, err = c.clusterServiceClient.UpdateExternalAuth(ctx, *externalAuthCSID, csExternalAuthBuilder)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update cluster-service ExternalAuth: %w", err))
	}

	logger.Info("requested cluster-service ExternalAuth update", "clusterServiceID", externalAuthCSID.String())

	serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch = desiredHash
	_, err = c.resourcesDBClient.ServiceProviderExternalAuths(
		externalAuth.ID.SubscriptionID,
		externalAuth.ID.ResourceGroupName,
		externalAuth.ID.Parent.Name,
		externalAuth.ID.Name,
	).Replace(ctx, serviceProviderExternalAuth, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderExternalAuth config hash: %w", err))
	}

	logger.Info("stored Cluster Service external auth updatable config hash", "hash", desiredHash, "version", storedVersion)
	return nil
}
