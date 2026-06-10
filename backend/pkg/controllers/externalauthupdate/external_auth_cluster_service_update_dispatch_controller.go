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

	desiredHash, err := ocm.ExternalAuthUpdatableConfigHash(externalAuth)
	if err != nil {
		return err
	}

	serviceProviderExternalAuth, err := database.GetOrCreateServiceProviderExternalAuth(ctx, c.resourcesDBClient, externalAuth.ID)
	if err != nil {
		return err
	}

	// For the old update approach, we introduce this mechanism to set the config hash in the ServiceProviderExternalAuth status when the controller runs
	// so we can compute the hash for pre-existing external auths.
	if !externalAuth.ServiceProviderProperties.UsesNewExternalAuthUpdateApproach {
		if serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch != desiredHash {
			logger.Info("using old update approach, skipping Cluster Service update but setting config hash", "desiredHash", desiredHash)
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
			return nil
		}
	}

	// If the desired hash matches the stored hash, we don't need to send an ExternalAuth CS update
	if serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch == desiredHash {
		return nil
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return err
	}

	externalAuthCSID := externalAuth.ServiceProviderProperties.ClusterServiceID
	logger.Info("dispatching external auth update to Cluster Service",
		"clusterServiceID", externalAuthCSID.String(),
		"previousHash", serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch,
		"desiredHash", desiredHash,
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

	logger.Info("stored Cluster Service external auth updatable config hash", "hash", desiredHash)
	return nil
}
