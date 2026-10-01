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

package controllerutils

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagedResourceGroupKey identifies a managed resource group discovered in Azure.
type ManagedResourceGroupKey struct {
	SubscriptionID    string `json:"subscriptionId"`
	ResourceGroupName string `json:"resourceGroupName"`
	ManagedBy         string `json:"managedBy"` // Resource ID of the managing cluster
	Location          string `json:"location"`
}

func (k ManagedResourceGroupKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.AddLogValuesForResourceIDString(k.ManagedBy)...,
	).WithValues(
		"resourceGroup", k.ResourceGroupName,
		"managedBy", k.ManagedBy,
		"location", k.Location,
	)
}

// ManagedResourceGroupWatchingControllerName is the name of the controller that
// discovers managed resource groups in Azure by watching subscription changes.
const ManagedResourceGroupWatchingControllerName = "ManagedResourceGroupWatching"

// ManagedResourceGroupDiscoveryControllerName identifies the subscription discovery queue and worker.
const ManagedResourceGroupDiscoveryControllerName = "ManagedResourceGroupDiscovery"

const managedResourceGroupDiscoveryTimeout = 5 * time.Minute

// ManagedResourceGroupSyncer processes discovered managed resource groups.
type ManagedResourceGroupSyncer interface {
	SyncOnce(ctx context.Context, key ManagedResourceGroupKey) error
}

type managedResourceGroupWatchingController struct {
	internalcontrollerutils.CacheSyncWaiter
	name                  string
	location              string
	syncer                ManagedResourceGroupSyncer
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
	subscriptionLister    corelisters.SubscriptionLister
	discoveryTimeout      time.Duration

	queue             workqueue.TypedRateLimitingInterface[ManagedResourceGroupKey]
	subscriptionQueue workqueue.TypedRateLimitingInterface[SubscriptionKey]
}

// NewManagedResourceGroupWatchingController creates a controller that discovers managed resource groups
// in Azure by watching subscription changes and listing resource groups for each subscription.
func NewManagedResourceGroupWatchingController(
	location string,
	syncer ManagedResourceGroupSyncer,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
	resyncDuration time.Duration,
) Controller {
	c := &managedResourceGroupWatchingController{
		name:                  ManagedResourceGroupWatchingControllerName,
		location:              location,
		syncer:                syncer,
		azureFPAClientBuilder: azureFPAClientBuilder,
		discoveryTimeout:      managedResourceGroupDiscoveryTimeout,
		subscriptionQueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[SubscriptionKey](),
			workqueue.TypedRateLimitingQueueConfig[SubscriptionKey]{
				Name: ManagedResourceGroupDiscoveryControllerName,
			},
		),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[ManagedResourceGroupKey](),
			workqueue.TypedRateLimitingQueueConfig[ManagedResourceGroupKey]{
				Name: ManagedResourceGroupWatchingControllerName,
			},
		),
	}

	subscriptionInformer, subscriptionLister := backendInformers.Subscriptions()
	c.subscriptionLister = subscriptionLister

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)

	_, err := subscriptionInformer.AddEventHandlerWithOptions(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.enqueueSubscription,
			UpdateFunc: c.enqueueSubscriptionUpdate,
		},
		cache.HandlerOptions{
			Logger:       &logger,
			ResyncPeriod: ptr.To(resyncDuration),
		})
	if err != nil {
		panic(err) // coding error
	}

	return c
}

func (c *managedResourceGroupWatchingController) enqueueSubscription(obj interface{}) {
	subscription := obj.(*coreapi.Subscription)
	c.subscriptionQueue.Add(SubscriptionKey{SubscriptionID: strings.ToLower(subscription.ResourceID.SubscriptionID)})
}

func (c *managedResourceGroupWatchingController) enqueueSubscriptionUpdate(oldObj, newObj interface{}) {
	c.enqueueSubscription(newObj)
}

func (c *managedResourceGroupWatchingController) discoverAndEnqueueManagedResourceGroups(ctx context.Context, key SubscriptionKey) error {
	ctx, cancel := context.WithTimeout(ctx, c.discoveryTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	logger := utils.LoggerFromContext(ctx)

	// Read the current subscription rather than retaining an informer event's snapshot across retries.
	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription for MRG discovery: %w", err))
	}

	subscriptionID := subscription.ResourceID.SubscriptionID
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		logger.Error(nil, "Subscription has no tenantId, skipping MRG discovery")
		return nil
	}
	tenantID := *subscription.Properties.TenantId

	rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, subscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create resource groups client: %w", err))
	}

	const resourceGroupListPageSize int32 = 100

	resourceGroupsPager := rgClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Top: ptr.To(resourceGroupListPageSize),
	})

	for resourceGroupsPager.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to list resource groups: %w", err))
		}

		for _, rg := range resourceGroupPage.Value {
			if rg.ManagedBy == nil || rg.Location == nil || rg.Name == nil {
				continue
			}

			if !strings.EqualFold(*rg.Location, c.location) {
				continue
			}

			parsedID, err := azcorearm.ParseResourceID(*rg.ManagedBy)
			if err != nil {
				continue
			}

			if !strings.EqualFold(parsedID.ResourceType.String(), coreapi.ClusterResourceType.String()) {
				continue
			}

			key := ManagedResourceGroupKey{
				SubscriptionID:    subscriptionID,
				ResourceGroupName: *rg.Name,
				ManagedBy:         *rg.ManagedBy,
				Location:          *rg.Location,
			}
			c.queue.Add(key)
		}
	}
	return ctx.Err()
}

func (c *managedResourceGroupWatchingController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	defer c.subscriptionQueue.ShutDown()

	if !c.WaitForCacheSync(ctx) {
		return
	}

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	var workers sync.WaitGroup
	// Keep discovery serialized as it was in the informer handler; MRG processing
	// remains independent so slow scans do not hold up already discovered groups.
	workers.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer workers.Done()
		c.runDiscoveryWorker(ctx)
	}()
	for i := 0; i < threadiness; i++ {
		workers.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer workers.Done()
			c.runWorker(ctx)
		}()
	}

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
	c.subscriptionQueue.ShutDown()
	c.queue.ShutDown()
	workers.Wait()
}

func (c *managedResourceGroupWatchingController) runDiscoveryWorker(ctx context.Context) {
	ctx = utils.ContextWithControllerName(ctx, ManagedResourceGroupDiscoveryControllerName)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(ManagedResourceGroupDiscoveryControllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	for c.processNextSubscription(ctx) {
	}
}

func (c *managedResourceGroupWatchingController) processNextSubscription(ctx context.Context) bool {
	key, shutdown := c.subscriptionQueue.Get()
	if shutdown {
		return false
	}
	defer c.subscriptionQueue.Done(key)
	if ctx.Err() != nil {
		return false
	}

	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)
	ReconcileTotal.WithLabelValues(ManagedResourceGroupDiscoveryControllerName).Inc()
	err := c.discoverAndEnqueueManagedResourceGroups(ctx, key)
	if err == nil {
		c.subscriptionQueue.Forget(key)
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	utilruntime.HandleErrorWithContext(ctx, err, "Error discovering managed resource groups; requeuing for later retry", "key", key)
	c.subscriptionQueue.AddRateLimited(key)
	return true
}

func (c *managedResourceGroupWatchingController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *managedResourceGroupWatchingController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)
	if ctx.Err() != nil {
		return false
	}

	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)

	ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.syncer.SyncOnce(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}
	if ctx.Err() != nil {
		return false
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error processing managed resource group; requeuing for later retry", "key", key)
	c.queue.AddRateLimited(key)

	return true
}

func (c *managedResourceGroupWatchingController) QueueForInformers(resyncDuration time.Duration, notifiers ...Notifier) error {
	panic("not implemented")
}

func (c *managedResourceGroupWatchingController) SyncOnce(ctx context.Context, keyObj any) error {
	panic("not implemented")
}
