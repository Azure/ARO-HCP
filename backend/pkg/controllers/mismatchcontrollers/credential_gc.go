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

package mismatchcontrollers

import (
	"context"
	"errors"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	CredentialGCControllerName = "CredentialGC"
	credentialMaxAge           = 48 * time.Hour
)

type credentialGCController struct {
	name string

	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	subscriptionLister listers.SubscriptionLister

	queue workqueue.TypedRateLimitingInterface[string]
}

func NewCredentialGCController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {
	c := &credentialGCController{
		name:               CredentialGCControllerName,
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		subscriptionLister: subscriptionLister,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: CredentialGCControllerName,
			},
		),
	}

	return c
}

func (c *credentialGCController) gcSubscription(ctx context.Context, subscriptionID string) error {
	logger := utils.LoggerFromContext(ctx)

	allClusters, err := c.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("listing clusters: %w", err))
	}

	now := c.clock.Now()
	errs := []error{}

	for _, cluster := range allClusters.Items(ctx) {
		credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(
			cluster.ID.SubscriptionID,
			cluster.ID.ResourceGroupName,
			cluster.ID.Name,
		)

		credIterator, err := credCRUD.List(ctx, nil)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("listing credentials for cluster %s: %w", cluster.ID.String(), err)))
			continue
		}

		for _, cred := range credIterator.Items(ctx) {
			age := now.Sub(cred.Spec.CreationTimestamp.Time)
			if age <= credentialMaxAge {
				continue
			}

			credLogger := logger.WithValues(
				"credentialName", cred.ResourceID.Name,
				"clusterResourceID", cluster.ID.String(),
				"age", age.String(),
			)
			credLogger.Info("deleting expired SystemAdminCredentialRequest")

			if err := credCRUD.Delete(ctx, cred.ResourceID.Name); err != nil {
				credLogger.Error(err, "unable to delete expired credential")
				errs = append(errs, utils.TrackError(fmt.Errorf("deleting credential %s: %w", cred.ResourceID.String(), err)))
			}
		}
		if err := credIterator.GetError(); err != nil {
			errs = append(errs, utils.TrackError(err))
		}
	}
	if err := allClusters.GetError(); err != nil {
		errs = append(errs, utils.TrackError(err))
	}

	return errors.Join(errs...)
}

func (c *credentialGCController) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	panic("not implemented")
}

func (c *credentialGCController) SyncOnce(ctx context.Context, keyObj any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.gcSubscription(ctx, keyObj.(string))
	if syncErr != nil {
		logger.Error(syncErr, "credential GC had errors")
	}

	return utils.TrackError(syncErr)
}

func (c *credentialGCController) queueAllSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error(err, "unable to list subscriptions")
	}
	for _, subscription := range allSubscriptions {
		c.queue.Add(subscription.ResourceID.SubscriptionID)
	}
}

func (c *credentialGCController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queueAllSubscriptions, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *credentialGCController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *credentialGCController) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddSubscriptionID(ref)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
