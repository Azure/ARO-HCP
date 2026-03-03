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

package clusterprovisioningcontrollers

import (
	"context"
	"fmt"
	"net/http"
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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DNSReservationKey is for driving workqueues keyed for DNS reservations
type DNSReservationKey struct {
	SubscriptionID     string `json:"subscriptionID"`
	DNSReservationName string `json:"dnsReservationName"`
}

func (k *DNSReservationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		"subscriptionID", k.SubscriptionID,
		"dnsReservationName", k.DNSReservationName,
	)
}

type dnsReservationCleanupController struct {
	name string

	clock           utilsclock.PassiveClock
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient

	queue workqueue.TypedRateLimitingInterface[DNSReservationKey]
}

// NewDNSReservationCleanupController creates a controller that watches DNSReservations
// and cleans up orphaned or expired reservations.
func NewDNSReservationCleanupController(
	cosmosClient database.DBClient,
	informers informers.BackendInformers,
) controllerutils.Controller {
	c := &dnsReservationCleanupController{
		name:            "DNSReservationCleanupController",
		clock:           utilsclock.RealClock{},
		cooldownChecker: controllerutils.NewTimeBasedCooldownChecker(1 * time.Hour),
		cosmosClient:    cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[DNSReservationKey](),
			workqueue.TypedRateLimitingQueueConfig[DNSReservationKey]{
				Name: "DNSReservationCleanupController",
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
	c.enqueueDNSReservation(obj.(*api.DNSReservation))
}

func (c *dnsReservationCleanupController) enqueueDNSReservationUpdate(_ interface{}, newObj interface{}) {
	c.enqueueDNSReservation(newObj.(*api.DNSReservation))
}

func (c *dnsReservationCleanupController) enqueueDNSReservation(dnsReservation *api.DNSReservation) {
	if dnsReservation.ResourceID == nil {
		return
	}

	key := DNSReservationKey{
		SubscriptionID:     dnsReservation.ResourceID.SubscriptionID,
		DNSReservationName: dnsReservation.ResourceID.Name,
	}

	logger := utils.DefaultLogger()
	logger = logger.WithValues("controller", c.name)
	ctx := logr.NewContext(context.TODO(), logger)
	logger = key.AddLoggerValues(logger)
	ctx = logr.NewContext(ctx, logger)

	if !c.cooldownChecker.CanSync(ctx, key) {
		return
	}

	c.queue.Add(key)
}

func (c *dnsReservationCleanupController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("controller", c.name)
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
	logger = key.AddLoggerValues(logger)
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

func (c *dnsReservationCleanupController) SyncOnce(ctx context.Context, keyObj any) error {
	logger := utils.LoggerFromContext(ctx)

	key := keyObj.(DNSReservationKey)

	dnsReservation, err := c.cosmosClient.DNSReservations(key.SubscriptionID).Get(ctx, key.DNSReservationName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get DNS reservation: %w", err))
	}

	now := c.clock.Now()

	// Case 1: if cleanupTime is non-nil and is in the past, log a message and delete the DNSReservation
	if dnsReservation.CleanupTime != nil && dnsReservation.CleanupTime.Time.Before(now) {
		logger.Info("cleanup time has passed, deleting DNS reservation", "cleanupTime", dnsReservation.CleanupTime.Time)
		if err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Delete(ctx, dnsReservation.ResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 2: if cleanupTime is non-nil and is in the future, return early
	if dnsReservation.CleanupTime != nil && dnsReservation.CleanupTime.After(now) {
		return nil
	}

	// Look up the owning ServiceProviderCluster using a live read from the database
	var owningServiceProviderCluster *api.ServiceProviderCluster
	if dnsReservation.OwningCluster != nil {
		owningServiceProviderCluster, err = c.cosmosClient.
			ServiceProviderClusters(dnsReservation.OwningCluster.SubscriptionID, dnsReservation.OwningCluster.ResourceGroupName, dnsReservation.OwningCluster.Name).
			Get(ctx, api.ServiceProviderClusterResourceName)
		if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
			return utils.TrackError(fmt.Errorf("failed to get owning service provider cluster: %w", err))
		}
	}

	// Case 3: if owningServiceProviderCluster does not exist and the dnsreservation is bound
	if owningServiceProviderCluster == nil && dnsReservation.BindingState == api.BindingStateBound {
		logger.Info("owning cluster no longer exists but DNS reservation is bound, marking for cleanup in one week")
		dnsReservation.CleanupTime = &metav1.Time{Time: now.Add(7 * 24 * time.Hour)}
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = api.BindingStatePendingDeletion
		if _, err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	// Case 4: if owningServiceProviderCluster does not exist and the dnsreservation is pending
	if owningServiceProviderCluster == nil && dnsReservation.BindingState == api.BindingStatePending {
		logger.Info("owning cluster does not exist and DNS reservation is pending, deleting unbound reservation")
		if err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Delete(ctx, dnsReservation.ResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// From here, owningServiceProviderCluster exists
	if owningServiceProviderCluster == nil {
		// This shouldn't happen given the cases above, but handle gracefully
		logger.Info("owning cluster is nil and binding state is not pending or bound, deleting unexpected case")
		if err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Delete(ctx, dnsReservation.ResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Check if the ServiceProviderCluster's KubeAPIServerDNSReservation points to this DNSReservation
	clusterPointsToThisDNSReservation := owningServiceProviderCluster.Status.KubeAPIServerDNSReservation != nil &&
		strings.EqualFold(owningServiceProviderCluster.Status.KubeAPIServerDNSReservation.String(), dnsReservation.ResourceID.String())
	clusterHasNoDNSReservation := owningServiceProviderCluster.Status.KubeAPIServerDNSReservation == nil
	clusterPointsToDifferentDNSReservation := !clusterHasNoDNSReservation && !clusterPointsToThisDNSReservation

	// Case 5: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation points to this DNSReservation and the state is bound
	if clusterPointsToThisDNSReservation && dnsReservation.BindingState == api.BindingStateBound {
		// Steady state, nothing to do
		logger.V(4).Info("DNS reservation is bound and cluster points to it, steady state")
		return nil
	}

	// Case 6: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation points to this DNSReservation and the state is anything besides bound
	if clusterPointsToThisDNSReservation && dnsReservation.BindingState != api.BindingStateBound {
		logger.Info("cluster points to this DNS reservation but state is not bound, fixing state", "currentState", dnsReservation.BindingState)
		dnsReservation.CleanupTime = nil
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = api.BindingStateBound
		if _, err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	// Case 7: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation is empty and bindingState is pending and mustBindByTime is not expired
	if clusterHasNoDNSReservation && dnsReservation.BindingState == api.BindingStatePending {
		if dnsReservation.MustBindByTime != nil && dnsReservation.MustBindByTime.After(now) {
			logger.Info("DNS reservation is pending and may still bind, waiting", "mustBindByTime", dnsReservation.MustBindByTime.Time)
			return nil
		}

		// Case 8: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation is empty and bindingState is pending and mustBindByTime is expired
		logger.Info("DNS reservation is pending but mustBindByTime has expired, deleting unbound reservation")
		if err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Delete(ctx, dnsReservation.ResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 9: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation points to different DNSReservation and bindingState is pending
	if clusterPointsToDifferentDNSReservation && dnsReservation.BindingState == api.BindingStatePending {
		logger.Info("cluster points to a different DNS reservation and this one is pending, deleting extra reservation", "clusterDNSReservation", owningServiceProviderCluster.Status.KubeAPIServerDNSReservation)
		if err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Delete(ctx, dnsReservation.ResourceID.Name); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete DNS reservation: %w", err))
		}
		return nil
	}

	// Case 10: if owningServiceProviderCluster exists and .status.KubeAPIServerDNSReservation points to different DNSReservation or empty and bindingState is bound
	if (clusterPointsToDifferentDNSReservation || clusterHasNoDNSReservation) && dnsReservation.BindingState == api.BindingStateBound {
		logger.Info("DNS reservation is bound but cluster points elsewhere or is empty, marking for cleanup in one week (cluster was likely deleted and recreated)", "clusterDNSReservation", owningServiceProviderCluster.Status.KubeAPIServerDNSReservation)
		dnsReservation.CleanupTime = &metav1.Time{Time: now.Add(7 * 24 * time.Hour)}
		dnsReservation.MustBindByTime = nil
		dnsReservation.BindingState = api.BindingStatePendingDeletion
		if _, err := c.cosmosClient.DNSReservations(dnsReservation.ResourceID.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update DNS reservation: %w", err))
		}
		return nil
	}

	logger.Info("Unexpected, no action defined yet")
	return nil
}
