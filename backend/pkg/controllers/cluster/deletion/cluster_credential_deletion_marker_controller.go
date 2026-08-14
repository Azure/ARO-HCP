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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterCredentialDeletionMarkerController stamps Status.DeletionTimestamp on
// every SystemAdminCredentialRequest and SystemAdminCredentialRevocation that
// belongs to a cluster being deleted. The existing per-credential and
// per-revocation deletion controllers then drive teardown of their respective
// desires and documents.
type clusterCredentialDeletionMarkerController struct {
	clock                      utilsclock.PassiveClock
	clusterLister              corelisters.ClusterLister
	credentialRequestLister    corelisters.SystemAdminCredentialRequestLister
	credentialRevocationLister corelisters.SystemAdminCredentialRevocationLister
	resourcesDBClient          corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterCredentialDeletionMarkerController)(nil)

func NewClusterCredentialDeletionMarkerController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, credentialRequestLister := informers.SystemAdminCredentialRequests()
	_, credentialRevocationLister := informers.SystemAdminCredentialRevocations()
	syncer := &clusterCredentialDeletionMarkerController{
		clock:                      clock,
		clusterLister:              clusterLister,
		credentialRequestLister:    credentialRequestLister,
		credentialRevocationLister: credentialRevocationLister,
		resourcesDBClient:          resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterCredentialDeletionMarkerController",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterCredentialDeletionMarkerController) NeedsWork(cluster *coreapi.HCPOpenShiftCluster) bool {
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil
}

func (c *clusterCredentialDeletionMarkerController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedCluster) {
		return nil
	}

	now := metav1.NewTime(c.clock.Now())

	cachedCreds, err := c.credentialRequestLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list credential requests: %w", err))
	}
	credCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRequests(key.HCPClusterName)
	for _, cachedCred := range cachedCreds {
		if cachedCred.Status.DeletionTimestamp != nil {
			continue
		}
		cred, err := credCRUD.Get(ctx, cachedCred.GetResourceID().Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			continue
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get credential request %s: %w", cachedCred.GetResourceID().Name, err))
		}
		if cred.Status.DeletionTimestamp != nil {
			continue
		}
		replacement := cred.DeepCopy()
		replacement.Status.DeletionTimestamp = &now
		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			if cosmosstorageutils.IsPreconditionFailedError(err) {
				continue
			}
			return utils.TrackError(fmt.Errorf("failed to mark credential request %s for deletion: %w", cred.GetResourceID().Name, err))
		}
		logger.Info("marked credential request for deletion", "credential", cred.GetResourceID().Name)
	}

	cachedRevocations, err := c.credentialRevocationLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list credential revocations: %w", err))
	}
	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRevocations(key.HCPClusterName)
	for _, cachedRevocation := range cachedRevocations {
		if cachedRevocation.Status.DeletionTimestamp != nil {
			continue
		}
		revocation, err := revocationCRUD.Get(ctx, cachedRevocation.GetResourceID().Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			continue
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get credential revocation %s: %w", cachedRevocation.GetResourceID().Name, err))
		}
		if revocation.Status.DeletionTimestamp != nil {
			continue
		}
		replacement := revocation.DeepCopy()
		replacement.Status.DeletionTimestamp = &now
		if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
			if cosmosstorageutils.IsPreconditionFailedError(err) {
				continue
			}
			return utils.TrackError(fmt.Errorf("failed to mark credential revocation %s for deletion: %w", revocation.GetResourceID().Name, err))
		}
		logger.Info("marked credential revocation for deletion", "revocation", revocation.GetResourceID().Name)
	}

	return nil
}
