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

package informers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type changeFeedInformer struct {
	name               string
	cosmosClient       database.DBClient
	queue              workqueue.TypedRateLimitingInterface[azcosmos.FeedRange]
	subscriptionLister listers.BasicReaderMaintainer[arm.Subscription]
	startFrom          time.Time

	// This is a map of feed ranges to continuation token strings.
	// No two worker goroutines should be processing the same feed
	// range concurrently so this falls within the specialized use
	// cases for sync.Map.
	//
	// At least as of v1.5.0-beta.4, the azcosmos module forces us
	// to use feed ranges when fetching a container's change feed.
	// Unclear if that requirement is permanent or if the API will
	// be simplified.
	continuationTokens sync.Map
}

func NewChangeFeedInformerController(
	cosmosClient database.DBClient,
	subscriptionLister listers.BasicReaderMaintainer[arm.Subscription],
) controllerutils.Controller {
	c := &changeFeedInformer{
		name:         "ChangeFeedInformer",
		cosmosClient: cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[azcosmos.FeedRange](),
			workqueue.TypedRateLimitingQueueConfig[azcosmos.FeedRange]{
				Name: "change-feed-informer",
			},
		),
		subscriptionLister: subscriptionLister,
		startFrom:          time.Now(),
	}

	return c
}

func (c *changeFeedInformer) processSubscriptionDocument(ctx context.Context, document json.RawMessage) error {
	var subscriptionDocument database.Subscription

	err := json.Unmarshal(document, &subscriptionDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	subscription, err := database.CosmosToInternalSubscription(&subscriptionDocument)
	if err != nil {
		return err
	}

	// FIXME Inform a subscription cache of the new or updated subscription.

	// temporary; do something with subscription
	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("Got %s", subscription.ResourceID))

	return nil
}

func (c *changeFeedInformer) processOperationDocument(ctx context.Context, document json.RawMessage) error {
	var operationDocument database.Operation

	err := json.Unmarshal(document, &operationDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	operation, err := database.CosmosToInternalOperation(&operationDocument)
	if err != nil {
		return err
	}

	// FIXME Dispatch the operation to a controller that will make the appropriate
	//       Cluster Service CRUD call. Perhaps use a publish/subscribe model with
	//       channels where controllers could subscribe to a central message bus
	//       that this controller publishes to?

	// temporary; do something with operation
	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("Got %s", operation.ResourceID))

	return nil
}

func (c *changeFeedInformer) processDocument(ctx context.Context, document json.RawMessage) error {
	var typedDocument database.TypedDocument

	// Unmarshal to a TypedDocument to peek at the resource type.
	err := json.Unmarshal(document, &typedDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	logger := utils.LoggerFromContext(ctx)
	logger = logger.With("resource_type", typedDocument.ResourceType)
	ctx = utils.ContextWithLogger(ctx, logger)

	// Potentially handle other resource types instead of polling Cosmos?
	switch typedDocument.ResourceType {
	case azcorearm.SubscriptionResourceType.String():
		err = c.processSubscriptionDocument(ctx, document)
	case api.OperationStatusResourceType.String():
		err = c.processOperationDocument(ctx, document)
	}

	return err
}

func (c *changeFeedInformer) SyncOnce(ctx context.Context, keyObj any) error {
	var changeFeedStatus int

	feedRange := keyObj.(azcosmos.FeedRange)

	for changeFeedStatus != http.StatusNotModified {
		options := &azcosmos.ChangeFeedOptions{}

		if continuation, ok := c.continuationTokens.Load(feedRange); ok {
			// Continue from a previous read of this feed range.
			options.Continuation = api.Ptr(continuation.(string))
		} else {
			// First read for this feed range.
			options.StartFrom = api.Ptr(c.startFrom)
			options.FeedRange = api.Ptr(feedRange)
		}

		response, err := c.cosmosClient.GetResourcesChangeFeed(ctx, options)
		if err != nil {
			return utils.TrackError(err)
		}

		if response.RawResponse.StatusCode == http.StatusOK {
			for _, doc := range response.Documents {
				err = c.processDocument(ctx, doc)
				if err != nil {
					return err
				}
			}
		}

		// Do not record the new continuation token until we have successfully
		// processed all documents from the change feed. This way we try again
		// on a processing error instead of just moving on.

		continuationToken, err := response.GetCompositeContinuationToken()
		if err != nil {
			return utils.TrackError(err)
		}

		c.continuationTokens.Store(feedRange, continuationToken)
	}

	return nil
}

func (c *changeFeedInformer) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.With("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// Enqueue feed ranges every few seconds. These trigger the workers
	// to read the Cosmos DB change feed for the provided feed range and
	// process any changes.
	go wait.JitterUntilWithContext(ctx, c.queueSync, 5*time.Second, 0.1, true)

	logger.Info(fmt.Sprintf("Started %d workers", threadiness))

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *changeFeedInformer) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *changeFeedInformer) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)

	c.queue.AddRateLimited(ref)

	return true
}

func (c *changeFeedInformer) queueSync(ctx context.Context) {
	for _, feedRange := range c.cosmosClient.GetResourcesFeedRanges() {
		c.queue.Add(feedRange)
	}
}
