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
	"time"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
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
	db            corecosmosstorage.ResourcesDBClient
	fleet         fleetcosmosstorage.FleetDBClient
	clusters      corelisters.ClusterLister
	providers     corelisters.ServiceProviderClusterLister
	subscriptions corelisters.SubscriptionLister
	management    fleetlisters.ManagementClusterLister
	claims        fleetlisters.DNSClaimLister
	client        ocm.ClusterServiceClientSpec
	clock         clock.PassiveClock
}

func NewDNSAllocationController(db corecosmosstorage.ResourcesDBClient, fleet fleetcosmosstorage.FleetDBClient, informers coreinformers.BackendInformers, fi fleetinformers.FleetInformers, client ocm.ClusterServiceClientSpec, clk clock.PassiveClock) controllerutils.Controller {
	_, clusters := informers.Clusters()
	_, providers := informers.ServiceProviderClusters()
	_, subscriptions := informers.Subscriptions()
	claimInformer, claims := fi.DNSClaims()
	_, management := fi.ManagementClusters()
	syncer := &dnsAllocationController{db: db, fleet: fleet, clusters: clusters, providers: providers, subscriptions: subscriptions, management: management, claims: claims, client: client, clock: clk}
	controller := controllerutils.NewClusterWatchingController(DNSAllocationControllerName, db, informers, nil, time.Minute, syncer)
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
	cluster, err := c.clusters.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	provider, providerErr := c.providers.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	subscription, subscriptionErr := c.subscriptions.Get(ctx, key.SubscriptionID)
	d := decideAllocationInputs(utils.LoggerFromContext(ctx), cluster, provider, providerErr, subscription, subscriptionErr, c.clock.Now(),
		func(stamp string) (*fleetapi.ManagementCluster, error) { return c.management.Get(ctx, stamp) },
		func(tenant string) (bool, error) {
			existing, err := ocm.FindClusterByAzureInfo(ctx, c.client, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, tenant, cluster.CustomerProperties.Platform.ManagedResourceGroup)
			return existing != nil, err
		},
		func(domain string) (*fleetapi.DNSClaim, error) { return c.claims.Get(ctx, domain) }, func() string { return rand.String(5) })
	return execute(ctx, c.db, c.fleet, d)
}
