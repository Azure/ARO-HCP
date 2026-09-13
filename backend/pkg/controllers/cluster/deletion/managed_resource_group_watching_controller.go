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

package deletion

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
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
		utils.LogValues{}.AddSubscriptionID(k.SubscriptionID)...,
	).WithValues(
		"resourceGroup", k.ResourceGroupName,
		"managedBy", k.ManagedBy,
		"location", k.Location,
	)
}

// ManagedResourceGroupProcessor processes discovered managed resource groups.
type ManagedResourceGroupProcessor interface {
	ProcessManagedResourceGroup(ctx context.Context, key ManagedResourceGroupKey) error
}

type managedResourceGroupWatchingController struct {
	name                  string
	location              string
	processor             ManagedResourceGroupProcessor
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder

	queue workqueue.TypedRateLimitingInterface[ManagedResourceGroupKey]
}

// NewManagedResourceGroupWatchingController creates a controller that discovers managed resource groups
// in Azure by watching subscription changes and listing resource groups for each subscription.
func NewManagedResourceGroupWatchingController(
	location string,
	processor ManagedResourceGroupProcessor,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
	resyncDuration time.Duration,
) controllerutils.Controller {
	c := &managedResourceGroupWatchingController{
		name:                  "ManagedResourceGroupWatching",
		location:              location,
		processor:             processor,
		resourcesDBClient:     resourcesDBClient,
		azureFPAClientBuilder: azureFPAClientBuilder,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[ManagedResourceGroupKey](),
			workqueue.TypedRateLimitingQueueConfig[ManagedResourceGroupKey]{
				Name: "ManagedResourceGroupWatching",
			},
		),
	}

	subscriptionInformer, _ := backendInformers.Subscriptions()

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
	c.discoverAndEnqueueManagedResourceGroups(context.Background(), subscription)
}

func (c *managedResourceGroupWatchingController) enqueueSubscriptionUpdate(oldObj, newObj interface{}) {
	c.enqueueSubscription(newObj)
}

func (c *managedResourceGroupWatchingController) discoverAndEnqueueManagedResourceGroups(ctx context.Context, subscription *coreapi.Subscription) {
	logger := utils.DefaultLogger()
	logger = logger.WithValues(
		utils.LogValues{}.
			AddControllerName(c.name).
			AddSubscriptionID(subscription.ResourceID.SubscriptionID)...,
	)
	ctx = utils.ContextWithLogger(ctx, logger)

	subscriptionID := subscription.ResourceID.SubscriptionID
	tenantID := *subscription.Properties.TenantId

	rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, subscriptionID)
	if err != nil {
		logger.Error(err, "Failed to create resource groups client")
		return
	}

	const resourceGroupListPageSize int32 = 100

	resourceGroupsPager := rgClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Top: ptr.To(resourceGroupListPageSize),
	})

	for resourceGroupsPager.More() {
		resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
		if err != nil {
			logger.Error(err, "Failed to list resource groups")
			return
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
}

func (c *managedResourceGroupWatchingController) Run(ctx context.Context, threadiness int) {
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

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
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

	logger := utils.LoggerFromContext(ctx)
	logger = key.AddLoggerValues(logger)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.processor.ProcessManagedResourceGroup(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error processing managed resource group; requeuing for later retry", "key", key)
	c.queue.AddRateLimited(key)

	return true
}

func (c *managedResourceGroupWatchingController) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	panic("not implemented")
}

func (c *managedResourceGroupWatchingController) SyncOnce(ctx context.Context, keyObj any) error {
	panic("not implemented")
}
