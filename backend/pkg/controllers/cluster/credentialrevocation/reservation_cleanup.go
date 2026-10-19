// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package credentialrevocation

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const SystemAdminCredentialRevocationReservationCleanupControllerName = "SystemAdminCredentialRevocationReservationCleanup"

type reservationCleanup struct {
	db          corecosmosstorage.ResourcesDBClient
	clusters    corelisters.ClusterLister
	revocations corelisters.SystemAdminCredentialRevocationLister
	clock       clock.PassiveClock
}

func NewReservationCleanupController(db corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, clk clock.PassiveClock) controllerutils.Controller {
	_, clusters := informers.Clusters()
	revocations, lister := informers.SystemAdminCredentialRevocations()
	syncer := &reservationCleanup{db: db, clusters: clusters, revocations: lister, clock: clk}
	controller := controllerutils.NewClusterWatchingController(SystemAdminCredentialRevocationReservationCleanupControllerName, db, informers, nil, time.Minute, syncer)
	queue := controller.(controllerutils.Enqueuer)
	onRevocation := func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		if rev, ok := obj.(*coreapi.SystemAdminCredentialRevocation); ok {
			id := rev.ResourceID.Parent
			queue.Enqueue(controllerutils.HCPClusterKey{SubscriptionID: id.SubscriptionID, ResourceGroupName: id.ResourceGroupName, HCPClusterName: id.Name})
		}
	}
	if _, err := revocations.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: onRevocation, DeleteFunc: onRevocation, UpdateFunc: func(old, new any) { onRevocation(old); onRevocation(new) }}); err != nil {
		panic(err)
	}
	return controller
}

type reservationObservation struct {
	Cluster          *coreapi.Cluster
	ClusterErr       error
	CachedRevocation *coreapi.SystemAdminCredentialRevocation
	CachedErr        error
	LiveRevocation   *coreapi.SystemAdminCredentialRevocation
	LiveErr          error
	FreshCluster     *coreapi.Cluster
	FreshErr         error
}

// decideReservationCleanup never uses an ARM operation to judge a new reservation.
// The age guard and live reads bound observation skew; they are not cross-document fencing.
func decideReservationCleanup(logger logr.Logger, o reservationObservation, now time.Time) ownershipDecision {
	if cosmosstorageutils.IsNotFoundError(o.ClusterErr) {
		return ownershipDecision{}
	}
	if o.ClusterErr != nil {
		return ownershipDecision{Err: o.ClusterErr}
	}
	if o.Cluster == nil {
		return ownershipDecision{}
	}
	props := o.Cluster.ServiceProviderProperties
	if props.SystemAdminCredentialRevocationResourceID == nil || props.SystemAdminCredentialRevocationRequestedAt == nil || props.DeletionTimestamp != nil {
		return ownershipDecision{} // LegacyReference/CosmosMigration supplies the original time, if available.
	}
	if o.CachedErr != nil && !cosmosstorageutils.IsNotFoundError(o.CachedErr) {
		return ownershipDecision{Err: o.CachedErr}
	}
	if o.CachedRevocation != nil {
		return ownershipDecision{}
	}
	if now.Before(props.SystemAdminCredentialRevocationRequestedAt.Add(coreapi.SystemAdminCredentialRevocationTimeout)) {
		return ownershipDecision{}
	}
	if o.LiveErr != nil && !cosmosstorageutils.IsNotFoundError(o.LiveErr) {
		return ownershipDecision{Err: o.LiveErr}
	}
	if o.LiveRevocation != nil || !cosmosstorageutils.IsNotFoundError(o.LiveErr) {
		return ownershipDecision{}
	}
	if cosmosstorageutils.IsNotFoundError(o.FreshErr) {
		return ownershipDecision{}
	}
	if o.FreshErr != nil {
		return ownershipDecision{Err: o.FreshErr}
	}
	if o.FreshCluster == nil {
		return ownershipDecision{}
	}
	fresh := o.FreshCluster.ServiceProviderProperties
	if o.FreshCluster.ID == nil || o.Cluster.ID == nil || !strings.EqualFold(o.FreshCluster.ID.String(), o.Cluster.ID.String()) ||
		fresh.DeletionTimestamp != nil || fresh.ClusterUID != props.ClusterUID || fresh.SystemAdminCredentialRevocationResourceID == nil ||
		!strings.EqualFold(fresh.SystemAdminCredentialRevocationResourceID.String(), props.SystemAdminCredentialRevocationResourceID.String()) ||
		!fresh.SystemAdminCredentialRevocationRequestedAt.Equal(props.SystemAdminCredentialRevocationRequestedAt) {
		return ownershipDecision{}
	}
	replacement := o.FreshCluster.DeepCopy()
	replacement.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = nil
	replacement.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = nil
	if fresh.RevokeCredentialsOperationID != "" {
		legacyID, err := coreapihelpers.SystemAdminCredentialRevocationID(o.FreshCluster.ID, fresh.RevokeCredentialsOperationID)
		if err != nil {
			return ownershipDecision{Err: err}
		}
		if strings.EqualFold(legacyID.String(), props.SystemAdminCredentialRevocationResourceID.String()) {
			replacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		}
	}
	logger.Info("Attempting to release undispatched revocation reservation", "revocation", props.SystemAdminCredentialRevocationResourceID.String(), "requestedAt", props.SystemAdminCredentialRevocationRequestedAt)
	return ownershipDecision{Cluster: replacement}
}

func (c *reservationCleanup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	o := reservationObservation{}
	o.Cluster, o.ClusterErr = c.clusters.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if o.Cluster != nil && o.Cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID != nil {
		id := o.Cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID
		o.CachedRevocation, o.CachedErr = c.revocations.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, id.Name)
		if cosmosstorageutils.IsNotFoundError(o.CachedErr) {
			crud := c.db.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
			o.LiveRevocation, o.LiveErr = crud.SystemAdminCredentialRevocations(key.HCPClusterName).Get(ctx, id.Name)
			o.FreshCluster, o.FreshErr = crud.Get(ctx, key.HCPClusterName)
		}
	}
	d := decideReservationCleanup(utils.LoggerFromContext(ctx), o, c.clock.Now())
	d.validate()
	if d.Cluster == nil {
		return d.Err
	}
	_, err := c.db.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, d.Cluster, nil)
	return err
}
