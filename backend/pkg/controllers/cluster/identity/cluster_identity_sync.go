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

package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ClusterIdentitySyncControllerName = "ClusterIdentitySync"

// clusterIdentitySyncer keeps ClientID/PrincipalID on
// HCPOpenShiftCluster.Identity.UserAssignedIdentities in sync with
// ServiceProviderCluster.Status.MSIManagedIdentities. It iterates the existing
// Identity map keys (preserving casing) and looks up each one in SPC by
// lowercased resource ID.
type clusterIdentitySyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterIdentitySyncer)(nil)

// NewClusterIdentitySyncController creates a new controller that continuously
// syncs Identity.UserAssignedIdentities ClientID/PrincipalID from
// ServiceProviderCluster.Status.MSIManagedIdentities.
//
// It compares Cluster.Identity against SPC and updates when ClientID/PrincipalID
// would change (including nil values returned when an identity does not exist).
// Map keys in Identity keep the casing from CustomerProperties; SPC lookups use
// lowercased resource IDs. Keys remain even when SPC does not yet have a matching
// identity entry. Deleting clusters are skipped.
func NewClusterIdentitySyncController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &clusterIdentitySyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		ClusterIdentitySyncControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		60*time.Minute, // Check every 60 minutes
		syncer,
	)

	return controller
}

func (c *clusterIdentitySyncer) NeedsWork(ctx context.Context, existingCluster *coreapi.HCPOpenShiftCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	if existingCluster.Identity == nil || len(existingCluster.Identity.UserAssignedIdentities) == 0 {
		return false
	}

	return true
}

// SyncOnce performs a single reconciliation of cluster identity information.
// It iterates Identity.UserAssignedIdentities, looks up each key (lowercased)
// in ServiceProviderCluster.Status.MSIManagedIdentities, and updates
// ClientID/PrincipalID when SPC has a match. Keys that are absent from SPC
// remain unchanged.
func (c *clusterIdentitySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// we'll be re-fired if it is created again
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(ctx, cachedCluster) {
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		// if the values don't change, then we still have no work to do.
		return nil
	}

	// Get the cluster from Cosmos
	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is ahead of the cache and obviates the need to do any work
	if !c.NeedsWork(ctx, existingCluster) {
		return nil
	}

	existingSPC, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// SPC may not exist yet; nothing to copy into Identity.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	replacement := existingCluster.DeepCopy()
	c.updateIdentityUserAssignedIdentitiesFromSPC(
		replacement.Identity.UserAssignedIdentities,
		existingSPC.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities,
		existingSPC.Status.MSIManagedIdentities.ServiceManagedIdentity,
	)

	if equality.Semantic.DeepEqual(existingCluster.Identity, replacement.Identity) {
		return nil
	}

	// Write the updated cluster back to Cosmos
	_, err = clusterCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced identity information from ServiceProviderCluster")
	return nil
}

// updateIdentityUserAssignedIdentitiesFromSPC walks the existing Identity map and, for
// each key, looks up the lowercased resource ID in SPC control-plane operator
// identities or the service managed identity. When found, ClientID and
// PrincipalID are updated in place. Keys missing from SPC are left as-is.
func (c *clusterIdentitySyncer) updateIdentityUserAssignedIdentitiesFromSPC(
	identityUserAssignedIdentities map[string]*coreapi.UserAssignedIdentity,
	spcControlPlaneOperatorsIdentities map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity,
	spcServiceManagedIdentity *coreapi.ServiceProviderClusterServiceManagedIdentity,
) {
	for identityResourceIDStr := range identityUserAssignedIdentities {
		lowerResourceIDStr := strings.ToLower(identityResourceIDStr)

		var clientID, principalID *string

		// If we found the identity in the SPC control plane operators identities, we use the ClientID and PrincipalID from the SPC.
		if spcIdentity, ok := spcControlPlaneOperatorsIdentities[lowerResourceIDStr]; ok && spcIdentity != nil {
			clientID = spcIdentity.ClientID
			principalID = spcIdentity.PrincipalID
			// If we found the identity in the SPC service managed identity, we use the ClientID and PrincipalID from the SPC.
		} else if spcServiceManagedIdentity != nil && spcServiceManagedIdentity.ResourceID != nil &&
			strings.ToLower(spcServiceManagedIdentity.ResourceID.String()) == lowerResourceIDStr {
			clientID = spcServiceManagedIdentity.ClientID
			principalID = spcServiceManagedIdentity.PrincipalID
		} else { // otherwise, we leave the identity as-is
			continue
		}

		// We initialize the identity if it does not exist, to avoid nil dereferencing
		if identityUserAssignedIdentities[identityResourceIDStr] == nil {
			identityUserAssignedIdentities[identityResourceIDStr] = &coreapi.UserAssignedIdentity{}
		}
		// We update the identity with the ClientID and PrincipalID values the SPC. Those values themselves
		// maybe be nil or empty strings.
		identityUserAssignedIdentities[identityResourceIDStr].ClientID = clientID
		identityUserAssignedIdentities[identityResourceIDStr].PrincipalID = principalID
	}
}
