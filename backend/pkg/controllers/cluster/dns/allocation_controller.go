// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const DNSAllocationControllerName = "ClusterDNSAllocation"

type dnsAllocationController struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	fleetDBClient                fleetcosmosstorage.FleetDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionsLister          corelisters.SubscriptionLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	dnsClaimLister               fleetlisters.DNSClaimLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	clock                        clock.PassiveClock
}

func NewDNSAllocationController(resourcesDBClient corecosmosstorage.ResourcesDBClient, fleetDBClient fleetcosmosstorage.FleetDBClient, backendInformers coreinformers.BackendInformers, fleetInformers fleetinformers.FleetInformers, clustersServiceClient ocm.ClusterServiceClientSpec, clock clock.PassiveClock) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionsLister := backendInformers.Subscriptions()
	claimInformer, dnsClaimLister := fleetInformers.DNSClaims()
	_, managementClusterLister := fleetInformers.ManagementClusters()
	syncer := &dnsAllocationController{resourcesDBClient: resourcesDBClient, fleetDBClient: fleetDBClient, clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderClusterLister, subscriptionsLister: subscriptionsLister, managementClusterLister: managementClusterLister, dnsClaimLister: dnsClaimLister, clustersServiceClient: clustersServiceClient, clock: clock}
	controller := controllerutils.NewClusterWatchingController(DNSAllocationControllerName, resourcesDBClient, backendInformers, nil, time.Minute, syncer)
	queue := controller.(controllerutils.Enqueuer)
	onClaim := func(obj any) {
		if key, ok := claimClusterKey(obj); ok {
			queue.Enqueue(key)
		}
	}
	if _, err := claimInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: onClaim, DeleteFunc: onClaim, UpdateFunc: func(old, new any) { onClaim(old); onClaim(new) }}); err != nil {
		panic(err)
	}
	return controller
}

func (c *dnsAllocationController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(cluster) {
		return nil
	}
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if serviceProviderCluster.Spec.ManagementClusterResourceID == nil {
		return nil
	}
	subscription, err := c.subscriptionsLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription has no tenant ID"))
	}
	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()
	existing, err := ocm.FindClusterByAzureInfo(csCtx, c.clustersServiceClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, *subscription.Properties.TenantId, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return utils.TrackError(err)
	}
	if existing != nil {
		// ClusterClusterServiceCreate adopts before ClusterDNSBackfill observes actual names.
		return nil
	}
	managementCluster, err := c.managementClusterLister.Get(ctx, serviceProviderCluster.Spec.ManagementClusterResourceID.Parent.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if managementCluster.Status.PublicDNSZoneResourceID == nil {
		return utils.TrackError(fmt.Errorf("selected management cluster has no public DNS zone"))
	}
	return c.allocate(ctx, cluster, serviceProviderCluster, managementCluster.Status.PublicDNSZoneResourceID.Name)
}

func (c *dnsAllocationController) needsWork(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil &&
		cluster.ServiceProviderProperties.PendingClusterServiceID != nil
}

// generateDNSSlug returns a five-character DNS label using Kubernetes' vowel-free
// alphabet to reduce the risk of offensive words. The length keeps backend names
// disjoint from CS's four-character allocations during migration. Fleet claims
// still arbitrate collisions between backend-generated names.
func generateDNSSlug() string {
	return rand.String(5)
}
