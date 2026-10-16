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
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	internalcontroller "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const CleanupControllerName = "DNSClaimCleanup"
const orphanClaimGrace = 30 * time.Minute

type claimKey struct{ BaseDomain string }

func (k claimKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("baseDomain", k.BaseDomain)
}

type cleanupController struct {
	fleet    fleetcosmosstorage.FleetDBClient
	claims   fleetlisters.DNSClaimLister
	clusters corelisters.ClusterLister
	clock    clock.PassiveClock
	queue    internalcontroller.AfterEnqueuer
}

// NewCleanupController reconciles reservation lifetimes against their ARO owners.
func NewCleanupController(fleet fleetcosmosstorage.FleetDBClient, informers fleetinformers.FleetInformers, core coreinformers.BackendInformers, clk clock.PassiveClock) controllerutils.Controller {
	informer, lister := informers.DNSClaims()
	clusterInformer, clusters := core.Clusters()
	syncer := &cleanupController{fleet: fleet, claims: lister, clusters: clusters, clock: clk}
	controller := internalcontroller.NewGenericWatchingController(CleanupControllerName, fleetapi.DNSClaimResourceType, syncer, controllerutils.ReconcileTotal)
	if err := controller.QueueForInformersWithMaxDepth(time.Minute, 0, informer); err != nil {
		panic(err)
	}
	syncer.queue = controller
	onCluster := func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		cluster, ok := obj.(*coreapi.Cluster)
		if !ok {
			return
		}
		for _, domain := range []string{cluster.ServiceProviderProperties.DNS.BaseDomain, cluster.ServiceProviderProperties.DNS.PendingBaseDomain} {
			if domain != "" {
				controller.Enqueue(claimKey{BaseDomain: domain})
			}
		}
	}
	if _, err := clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: onCluster, DeleteFunc: onCluster, UpdateFunc: func(old, new any) { onCluster(old); onCluster(new) }}); err != nil {
		panic(err)
	}
	return controller
}
func (c *cleanupController) MakeKey(id *azcorearm.ResourceID) claimKey {
	return claimKey{BaseDomain: id.Name + "." + id.Parent.Name}
}
func (c *cleanupController) CooldownChecker() internalcontroller.CooldownChecker { return nil }

func decideCleanup(logger logr.Logger, claim *fleetapi.DNSClaim, cluster *coreapi.Cluster, observationErr error, now time.Time) decision {
	if observationErr != nil && !cosmosstorageutils.IsNotFoundError(observationErr) {
		return decision{Action: noWrite, Err: observationErr}
	}
	if cluster != nil {
		uid := cluster.ServiceProviderProperties.ClusterUID
		if uid == "" {
			// BackfillClusterUID may not have populated this parent observation yet.
			return decision{Action: noWrite}
		}
		if uid == claim.Owner {
			if cluster.ServiceProviderProperties.DeletionTimestamp == nil || cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil || cluster.ServiceProviderProperties.ClusterServiceID != nil || cluster.ServiceProviderProperties.PendingClusterServiceID != nil {
				return decision{Action: noWrite}
			}
			logger.Info("Owner is deleting and CS cleanup completed; releasing DNS reservation", "baseDomain", claim.BaseDomain, "clusterUID", uid)
			return decision{Action: deleteClaim, Claim: claim}
		}
	}
	remaining := claim.CreatedAt.Add(orphanClaimGrace).Sub(now)
	if remaining > 0 {
		logger.V(2).Info("Apparent DNS orphan is too young; allowing owner cache to catch up", "baseDomain", claim.BaseDomain, "remaining", remaining)
		return decision{Action: noWrite, RequeueAfter: remaining}
	}
	logger.Info("DNS owner absent or replaced beyond orphan grace; releasing reservation", "baseDomain", claim.BaseDomain, "clusterUID", claim.Owner, "createdAt", claim.CreatedAt)
	return decision{Action: deleteClaim, Claim: claim}
}

func (c *cleanupController) SyncOnce(ctx context.Context, key claimKey) error {
	claim, err := c.claims.Get(ctx, key.BaseDomain)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	id, err := azcorearm.ParseResourceID(claim.ClusterResourceID)
	if err != nil {
		return fmt.Errorf("invalid DNS claim owner resource ID: %w", err)
	}
	cluster, observationErr := c.clusters.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	d := decideCleanup(utils.LoggerFromContext(ctx), claim, cluster, observationErr, c.clock.Now())
	d.validate()
	switch d.Action {
	case noWrite:
		if d.RequeueAfter > 0 {
			c.queue.EnqueueAfter(key, d.RequeueAfter)
		}
		return d.Err
	case deleteClaim:
		return c.fleet.DNSClaims(strings.ToLower(d.Claim.ResourceID.Parent.Name)).Delete(ctx, d.Claim.ResourceID.Name, &azcosmos.ItemOptions{IfMatchEtag: &d.Claim.CosmosETag})
	default:
		panic("unexpected DNS cleanup mutation")
	}
}
