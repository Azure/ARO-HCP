// Copyright 2025 Microsoft Corporation
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

package dnsreservation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DNSReservationCleanupControllerName is the single source of truth for this
// controller's name; it feeds the workqueue metric label, the ctx controller
// name, and the log fields.
const DNSReservationCleanupControllerName = "DNSReservationCleanupController"

// oneWeek is the cooldown applied before a freed DNS name may be reused. When a
// bound reservation's cluster goes away (or moves to a different name), we do not
// delete the reservation immediately; we mark it PendingDeletion with a
// CleanupTime one week out so DNS resolvers/customers that still cache the old
// name cannot collide with a brand-new cluster that reuses it.
const oneWeek = 7 * 24 * time.Hour

// DNSReservationKey is the workqueue key for DNS reservation cleanup. Reservations
// are subscription-scoped, so the key is (subscriptionID, reservationName).
type DNSReservationKey struct {
	SubscriptionID     string `json:"subscriptionID"`
	DNSReservationName string `json:"dnsReservationName"`
}

// AddLoggerValues implements utils.LoggableKey so every log line from a reconcile
// of this key carries the same fields.
func (k DNSReservationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		"subscriptionID", k.SubscriptionID,
		"dnsReservationName", k.DNSReservationName,
	)
}

// dnsReservationCleanupController watches DNSReservation documents and reaps
// orphaned, expired, or superseded reservations. Unlike the creation controller,
// it is keyed on the reservation itself (not a cluster), so it implements the
// full controllerutils.Controller interface directly (own workqueue + workers)
// rather than going through NewClusterWatchingController.
type dnsReservationCleanupController struct {
	name string

	clock             utilsclock.PassiveClock
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient corecosmosstorage.ResourcesDBClient

	queue workqueue.TypedRateLimitingInterface[DNSReservationKey]
}

var _ controllerutils.Controller = &dnsReservationCleanupController{}

// NewDNSReservationCleanupController creates a controller that watches
// DNSReservations and cleans up orphaned or expired reservations. A one-hour
// time-based cooldown throttles re-enqueues of the same reservation so the coarse
// informer resync does not hot-loop the reconcile.
func NewDNSReservationCleanupController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	c := &dnsReservationCleanupController{
		name:              DNSReservationCleanupControllerName,
		clock:             utilsclock.RealClock{},
		cooldownChecker:   controllerutil.NewTimeBasedCooldownChecker(1 * time.Hour),
		resourcesDBClient: resourcesDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[DNSReservationKey](),
			workqueue.TypedRateLimitingQueueConfig[DNSReservationKey]{
				Name: DNSReservationCleanupControllerName,
			},
		),
	}

	dnsReservationInformer, _ := informers.DNSReservations()
	_, err := dnsReservationInformer.AddEventHandlerWithOptions(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.enqueueDNSReservationAdd,
			UpdateFunc: c.enqueueDNSReservationUpdate,
		},
		cache.HandlerOptions{
			ResyncPeriod: ptr.To(1 * time.Hour),
		})
	if err != nil {
		panic(err) // coding error
	}

	return c
}

func (c *dnsReservationCleanupController) enqueueDNSReservationAdd(obj interface{}) {
	c.enqueueDNSReservation(obj.(*coreapi.DNSReservation))
}

func (c *dnsReservationCleanupController) enqueueDNSReservationUpdate(_ interface{}, newObj interface{}) {
	c.enqueueDNSReservation(newObj.(*coreapi.DNSReservation))
}

func (c *dnsReservationCleanupController) enqueueDNSReservation(dnsReservation *coreapi.DNSReservation) {
	resourceID := dnsReservation.GetResourceID()
	if resourceID == nil {
		return
	}

	key := DNSReservationKey{
		SubscriptionID:     resourceID.SubscriptionID,
		DNSReservationName: resourceID.Name,
	}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	logger = key.AddLoggerValues(logger)
	ctx := utils.ContextWithLogger(context.TODO(), logger)

	// Throttle re-enqueues: the informer resync fires every reservation
	// periodically, but cleanup decisions change slowly, so the cooldown avoids
	// hot-looping. Errors still requeue immediately via AddRateLimited.
	if !c.cooldownChecker.CanSync(ctx, key) {
		return
	}

	c.queue.Add(key)
}

// QueueForInformers is a no-op for this controller: it registers its own
// DNSReservation informer event handler in NewDNSReservationCleanupController.
// The method exists only to satisfy the controllerutils.Controller interface.
func (c *dnsReservationCleanupController) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	return nil
}

func (c *dnsReservationCleanupController) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *dnsReservationCleanupController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *dnsReservationCleanupController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	logger := utils.LoggerFromContext(ctx)
	logger = utils.AddLoggerValues(logger, key)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", key)
	c.queue.AddRateLimited(key)

	return true
}

// SyncOnce evaluates a single DNSReservation against the ten cleanup cases and
// drives it toward the correct terminal state. It is the eventual reaper of every
// reservation the creation controller makes: no reservation is ever hard-deleted
// by the creation path.
//
// Two clocks matter here:
//   - MustBindByTime (set to now+61m at creation) bounds how long a *Pending*
//     reservation may sit unclaimed. If a cluster never points at it by then, the
//     name is freed immediately (case 8) — this cleans up the losers of a
//     create-time conflict retry, which are left Pending and orphaned.
//   - CleanupTime (set to now+1week when a *Bound* reservation's cluster goes
//     away or moves) implements the DNS-name reuse cooldown. The name is not
//     returned to the pool until a full week has elapsed (case 1), preventing a
//     freshly-created cluster from re-acquiring a name still cached by resolvers.
//
// Full lifecycle:  Pending ─▶ Bound ─▶ PendingDeletion ─▶ deleted, with several
// self-healing shortcuts (cases 4, 6, 8, 9) for crash/partial-write recovery.
// The ServiceProviderCluster.Status.KubeAPIServerDNSReservation pointer is the
// source of truth for "does a live cluster still want this name".
func (c *dnsReservationCleanupController) SyncOnce(ctx context.Context, keyObj any) error {
	logger := utils.LoggerFromContext(ctx)

	key := keyObj.(DNSReservationKey)

	dnsReservation, err := c.resourcesDBClient.DNSReservations(key.SubscriptionID).Get(ctx, key.DNSReservationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // already reaped, nothing to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get DNS reservation: %w", err))
	}

	now := c.clock.Now()
	dnsReservationResourceID := dnsReservation.GetResourceID()

	// Case 1: CleanupTime set and in the past → the one-week cooldown has elapsed,
	// so the name may be reused. Delete the reservation, returning the name to the pool.
	if dnsReservation.CleanupTime != nil && dnsReservation.CleanupTime.Time.Before(now) {
		logger.Info("cleanup time has passed, deleting DNS reservation", "cleanupTime", dnsReservation.CleanupTime.Time)
		if err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Delete(ctx, dnsReservationResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 2: CleanupTime set and in the future → still cooling down, wait.
	if dnsReservation.CleanupTime != nil && dnsReservation.CleanupTime.After(now) {
		return nil
	}

	// Look up the owning ServiceProviderCluster with a live read. Its
	// Status.KubeAPIServerDNSReservation pointer tells us whether the cluster still
	// wants this exact reservation.
	var owningServiceProviderCluster *coreapi.ServiceProviderCluster
	if dnsReservation.OwningCluster != nil {
		owningServiceProviderCluster, err = c.resourcesDBClient.
			ServiceProviderClusters(dnsReservation.OwningCluster.SubscriptionID, dnsReservation.OwningCluster.ResourceGroupName, dnsReservation.OwningCluster.Name).
			Get(ctx, coreapi.ServiceProviderClusterResourceName)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to get owning service provider cluster: %w", err))
		}
	}

	// Case 3: owning cluster is gone and the reservation is Bound → the cluster was
	// deleted while actively using this name. Start the one-week reuse cooldown.
	if owningServiceProviderCluster == nil && dnsReservation.BindingState == coreapi.BindingStateBound {
		logger.Info("owning cluster no longer exists but DNS reservation is bound, marking for cleanup in one week")
		dnsReservation.CleanupTime = &metav1.Time{Time: now.Add(oneWeek)}
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = coreapi.BindingStatePendingDeletion
		if _, err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	// Case 4: owning cluster is gone and the reservation is still Pending → it was
	// never bound, so there is nothing to cool down. Delete immediately.
	if owningServiceProviderCluster == nil && dnsReservation.BindingState == coreapi.BindingStatePending {
		logger.Info("owning cluster does not exist and DNS reservation is pending, deleting unbound reservation")
		if err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Delete(ctx, dnsReservationResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// From here on, the owning ServiceProviderCluster exists. Guard against the
	// unexpected state (owner nil but binding state neither Pending nor Bound, e.g.
	// a leftover PendingDeletion with no CleanupTime): delete it defensively.
	if owningServiceProviderCluster == nil {
		logger.Info("owning cluster is nil and binding state is not pending or bound, deleting unexpected reservation", "bindingState", dnsReservation.BindingState)
		if err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Delete(ctx, dnsReservationResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Classify what the cluster's pointer says relative to this reservation.
	clusterPointsToThisDNSReservation := owningServiceProviderCluster.Status.KubeAPIServerDNSReservation != nil &&
		strings.EqualFold(owningServiceProviderCluster.Status.KubeAPIServerDNSReservation.String(), dnsReservationResourceID.String())
	clusterHasNoDNSReservation := owningServiceProviderCluster.Status.KubeAPIServerDNSReservation == nil
	clusterPointsToDifferentDNSReservation := !clusterHasNoDNSReservation && !clusterPointsToThisDNSReservation

	// Case 5: cluster points to this reservation and it is Bound → steady state.
	if clusterPointsToThisDNSReservation && dnsReservation.BindingState == coreapi.BindingStateBound {
		logger.V(4).Info("DNS reservation is bound and cluster points to it, steady state")
		return nil
	}

	// Case 6: cluster points to this reservation but the state is not Bound → the
	// creation controller's best-effort bind write failed; fix the state to Bound.
	if clusterPointsToThisDNSReservation && dnsReservation.BindingState != coreapi.BindingStateBound {
		logger.Info("cluster points to this DNS reservation but state is not bound, fixing state", "currentState", dnsReservation.BindingState)
		dnsReservation.CleanupTime = nil
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = coreapi.BindingStateBound
		if _, err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	// Case 7: cluster has no reservation pointer yet, this one is Pending, and
	// MustBindByTime has not passed → the cluster may still bind it. Wait.
	if clusterHasNoDNSReservation && dnsReservation.BindingState == coreapi.BindingStatePending {
		if dnsReservation.MustBindByTime != nil && dnsReservation.MustBindByTime.After(now) {
			logger.Info("DNS reservation is pending and may still bind, waiting", "mustBindByTime", dnsReservation.MustBindByTime.Time)
			return nil
		}

		// Case 8: same as case 7 but MustBindByTime has expired → the cluster never
		// bound this name (typically a conflict-retry loser). Free the name now.
		logger.Info("DNS reservation is pending but mustBindByTime has expired, deleting unbound reservation")
		if err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Delete(ctx, dnsReservationResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 9: cluster points at a DIFFERENT reservation and this one is Pending →
	// this is an extra, never-bound reservation (the cluster chose another name).
	// Delete it immediately; there is nothing to cool down.
	if clusterPointsToDifferentDNSReservation && dnsReservation.BindingState == coreapi.BindingStatePending {
		logger.Info("cluster points to a different DNS reservation and this one is pending, deleting extra reservation", "clusterDNSReservation", owningServiceProviderCluster.Status.KubeAPIServerDNSReservation)
		if err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Delete(ctx, dnsReservationResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 10: this reservation is Bound but the cluster now points elsewhere or has
	// no pointer at all → the cluster was likely deleted and recreated (getting a
	// new name). Start the one-week reuse cooldown on this old, now-detached name.
	if (clusterPointsToDifferentDNSReservation || clusterHasNoDNSReservation) && dnsReservation.BindingState == coreapi.BindingStateBound {
		logger.Info("DNS reservation is bound but cluster points elsewhere or is empty, marking for cleanup in one week (cluster was likely deleted and recreated)", "clusterDNSReservation", owningServiceProviderCluster.Status.KubeAPIServerDNSReservation)
		dnsReservation.CleanupTime = &metav1.Time{Time: now.Add(oneWeek)}
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = coreapi.BindingStatePendingDeletion
		if _, err := c.resourcesDBClient.DNSReservations(dnsReservationResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	logger.Info("no cleanup action matched for DNS reservation", "bindingState", dnsReservation.BindingState)
	return nil
}
