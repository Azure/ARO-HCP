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

package creation

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createServiceProviderClusterSyncer ensures a ServiceProviderCluster document
// exists for every HCPCluster. Consumer backend controllers (validation,
// version, etc.) read the ServiceProviderCluster through a cached lister and
// bail out when it is missing. Frontend admission also reads the cached document
// and fails when required provider state is unavailable; initialization belongs
// to the backend, not admission.
type createServiceProviderClusterSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*createServiceProviderClusterSyncer)(nil)

const CreateServiceProviderClusterControllerName = "CreateServiceProviderCluster"

// NewCreateServiceProviderClusterController wires the controller that creates
// missing ServiceProviderCluster documents.
func NewCreateServiceProviderClusterController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &createServiceProviderClusterSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	controller := controllerutils.NewClusterWatchingController(
		CreateServiceProviderClusterControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)

	return controller
}

// SyncOnce ensures provider state exists and mirrors both legacy cluster IDs.
// Equal cached state avoids writes. Live source and destination ETags protect
// backfill; missing provider state is not recreated after completed cleanup.
func (c *createServiceProviderClusterSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCPCluster from lister: %w", err))
	}

	provider, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	properties := existingCluster.ServiceProviderProperties
	idsCanonical := (properties.PendingClusterServiceID == nil || len(properties.PendingClusterServiceID.String()) > 0) && (properties.ClusterServiceID == nil || len(properties.ClusterServiceID.String()) > 0)
	deletionComplete := properties.DeletionTimestamp != nil && properties.ClusterServiceDeletionTimestamp != nil && properties.PendingClusterServiceID == nil && properties.ClusterServiceID == nil
	markersSynced := properties.ServiceProviderClusterInitialized && (!deletionComplete || properties.ClusterServiceDeletionCompleted)
	if err == nil && idsCanonical && markersSynced && reflect.DeepEqual(provider.PendingClusterServiceID, properties.PendingClusterServiceID) && reflect.DeepEqual(provider.ClusterServiceID, properties.ClusterServiceID) {
		return nil
	}
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from lister: %w", err))
	}

	if err := corecosmosstorage.SyncClusterServiceIDs(ctx, c.resourcesDBClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName); err != nil {
		return utils.TrackError(fmt.Errorf("failed to synchronize ServiceProviderCluster: %w", err))
	}

	return nil
}
