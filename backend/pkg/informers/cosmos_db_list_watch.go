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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const feedRangePollInterval = 1 * time.Second

// toKey converts an Azure resource ID to a lowercase
// string for use as an identifying key for a resource.
func toKey(resourceID *azcorearm.ResourceID) string {
	return strings.ToLower(resourceID.String())
}

// watcher implements watch.Interface and serves as the backing store
// for a ListWatch instance. The duration of a watch is limited by an
// embedded ExpiringWatcher.
type watcher struct {
	id       string
	expiry   time.Duration
	expiring watch.Interface
	result   chan watch.Event

	// knownKeys allows us to track whether a change feed event
	// is a new or an updated document, since at the moment we're
	// stuck with the "latest version" change feed mode that does
	// not distinguish. This all gets easier if and when the "all
	// versions and deletes" mode becomes available in the Go SDK.
	knownKeys sets.Set[string]
}

func newWatcher(expiry time.Duration) *watcher {
	return &watcher{
		id:        uuid.New().String(),
		expiry:    expiry,
		knownKeys: sets.New[string](),
	}
}

// reset prepares the watcher for a new watch.
func (w *watcher) reset(ctx context.Context) {
	w.expiring = NewExpiringWatcher(ctx, w.expiry)
	w.result = make(chan watch.Event)
}

// run waits for the embedded ExpiringWatcher to terminate and propagates
// its one and only event: a watch expired error.
func (w *watcher) run() {
	if event, ok := <-w.expiring.ResultChan(); ok {
		w.result <- event
	}
}

// newEvent creates a watch event for the given object. The event type
// is Added or Modified depending on whether the key has been previously
// seen. Deleted events are not currently supported.
func (w *watcher) newEvent(key string, object runtime.Object) watch.Event {
	if w.knownKeys.Has(key) {
		return watch.Event{Type: watch.Modified, Object: object}
	}

	w.knownKeys.Insert(key)
	return watch.Event{Type: watch.Added, Object: object}
}

func (w *watcher) Stop() {
	w.expiring.Stop()
}

func (w *watcher) ResultChan() <-chan watch.Event {
	return w.result
}

// watcherSet holds a set of watchers for a particular resource type.
type watcherSet struct {
	mutex sync.Mutex
	// The map key is watcher.id, which itself is a UUID. The key
	// value is meaningless, just needs to be unique and comparable.
	watchers map[string]*watcher
}

func newWatcherSet() *watcherSet {
	return &watcherSet{
		watchers: make(map[string]*watcher),
	}
}

// runWatcher allows the given watcher to publish change feed events
// until its embedded ExpiringWatcher expires.
func (c *watcherSet) runWatcher(ctx context.Context, watcher *watcher) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	watcher.reset(ctx)

	// Subscribe to change feed events.
	c.watchers[watcher.id] = watcher

	go func() {
		watcher.run()

		c.mutex.Lock()
		defer c.mutex.Unlock()

		// Close the result channel with the mutex locked so we
		// don't race with change feed events being written to it.
		close(watcher.result)

		// Unsubscribe from change feed events.
		delete(c.watchers, watcher.id)
	}()
}

type CosmosDBListWatch struct {
	name         string
	cosmosClient database.DBClient
	queue        workqueue.TypedRateLimitingInterface[azcosmos.FeedRange]
	startFrom    time.Time

	subscriptions           *watcherSet
	clusters                *watcherSet
	nodePools               *watcherSet
	externalAuths           *watcherSet
	serviceProviderClusters *watcherSet
	operations              *watcherSet

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

func NewCosmosDBListWatch(cosmosClient database.DBClient) *CosmosDBListWatch {
	c := &CosmosDBListWatch{
		name:         "CosmosDBListWatch",
		cosmosClient: cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[azcosmos.FeedRange](),
			workqueue.TypedRateLimitingQueueConfig[azcosmos.FeedRange]{
				Name: "cosmos-db-list-watch",
			},
		),
		startFrom: time.Now().UTC(),

		subscriptions:           newWatcherSet(),
		clusters:                newWatcherSet(),
		nodePools:               newWatcherSet(),
		externalAuths:           newWatcherSet(),
		serviceProviderClusters: newWatcherSet(),
		operations:              newWatcherSet(),
	}

	return c
}

func (c *CosmosDBListWatch) NewSubscriptionInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing subscriptions")
			defer logger.Info("finished listing subscriptions")

			iter, err := c.cosmosClient.GlobalListers().Subscriptions().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &arm.SubscriptionList{}
			list.ResourceVersion = "0"
			for _, sub := range iter.Items(ctx) {
				list.Items = append(list.Items, *sub)
				watcher.knownKeys.Insert(toKey(sub.ResourceID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.subscriptions.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&arm.Subscription{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) NewClusterInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing clusters")
			defer logger.Info("finished listing clusters")

			iter, err := c.cosmosClient.GlobalListers().Clusters().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &api.HCPOpenShiftClusterList{}
			list.ResourceVersion = "0"
			for _, cluster := range iter.Items(ctx) {
				list.Items = append(list.Items, *cluster)
				watcher.knownKeys.Insert(toKey(cluster.ID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.clusters.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.HCPOpenShiftCluster{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) NewNodePoolInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing node pools")
			defer logger.Info("finished listing node pools")

			iter, err := c.cosmosClient.GlobalListers().NodePools().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &api.HCPOpenShiftClusterNodePoolList{}
			list.ResourceVersion = "0"
			for _, np := range iter.Items(ctx) {
				list.Items = append(list.Items, *np)
				watcher.knownKeys.Insert(toKey(np.ID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.nodePools.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.HCPOpenShiftClusterNodePool{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) NewExternalAuthInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing external auths")
			defer logger.Info("finished listing external auths")

			iter, err := c.cosmosClient.GlobalListers().ExternalAuths().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &api.HCPOpenShiftClusterExternalAuthList{}
			list.ResourceVersion = "0"
			for _, ea := range iter.Items(ctx) {
				list.Items = append(list.Items, *ea)
				watcher.knownKeys.Insert(toKey(ea.ID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.externalAuths.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.HCPOpenShiftClusterExternalAuth{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) NewServiceProviderClusterInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing service provider clusters")
			defer logger.Info("finished listing service provider clusters")

			iter, err := c.cosmosClient.GlobalListers().ServiceProviderClusters().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &api.ServiceProviderClusterList{}
			list.ResourceVersion = "0"
			for _, spc := range iter.Items(ctx) {
				list.Items = append(list.Items, *spc)
				watcher.knownKeys.Insert(toKey(&spc.ResourceID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.serviceProviderClusters.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.ServiceProviderCluster{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) NewActiveOperationInformer(relistDuration time.Duration, informerOptions cache.SharedIndexInformerOptions) cache.SharedIndexInformer {
	watcher := newWatcher(relistDuration)

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing active operations")
			defer logger.Info("finished listing active operations")

			iter, err := c.cosmosClient.GlobalListers().ActiveOperations().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			watcher.knownKeys.Clear()

			list := &api.OperationList{}
			list.ResourceVersion = "0"
			for _, op := range iter.Items(ctx) {
				list.Items = append(list.Items, *op)
				watcher.knownKeys.Insert(toKey(op.ResourceID))
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			c.operations.runWatcher(ctx, watcher)
			return watcher, nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.Operation{},
		informerOptions,
	)
}

func (c *CosmosDBListWatch) processSubscriptionDocument(ctx context.Context, document json.RawMessage) error {
	var subscriptionDocument database.GenericDocument[arm.Subscription]

	err := json.Unmarshal(document, &subscriptionDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	subscription := &subscriptionDocument.Content

	key := toKey(subscription.ResourceID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", subscription.ResourceID.String(),
		"subscription_id", subscription.ResourceID.SubscriptionID,
	)

	c.subscriptions.mutex.Lock()
	defer c.subscriptions.mutex.Unlock()

	if len(c.subscriptions.watchers) > 0 {
		for _, watcher := range c.subscriptions.watchers {
			event := watcher.newEvent(key, subscription)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processClusterDocument(ctx context.Context, document json.RawMessage) error {
	var clusterDocument database.HCPCluster

	err := json.Unmarshal(document, &clusterDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	cluster, err := database.CosmosToInternalCluster(&clusterDocument)
	if err != nil {
		return err
	}

	key := toKey(cluster.ID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", cluster.ID.String(),
		"subscription_id", cluster.ID.SubscriptionID,
		"resource_group", cluster.ID.ResourceGroupName,
		"resource_name", cluster.ID.Name,
		"hcp_cluster_name", cluster.ID.Name,
	)

	c.clusters.mutex.Lock()
	defer c.clusters.mutex.Unlock()

	if len(c.clusters.watchers) > 0 {
		for _, watcher := range c.clusters.watchers {
			event := watcher.newEvent(key, cluster)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processNodePoolDocument(ctx context.Context, document json.RawMessage) error {
	var nodePoolDocument database.NodePool

	err := json.Unmarshal(document, &nodePoolDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	nodePool, err := database.CosmosToInternalNodePool(&nodePoolDocument)
	if err != nil {
		return err
	}

	key := toKey(nodePool.ID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", nodePool.ID.String(),
		"subscription_id", nodePool.ID.SubscriptionID,
		"resource_group", nodePool.ID.ResourceGroupName,
		"resource_name", nodePool.ID.Name,
		"hcp_cluster_name", nodePool.ID.Parent.Name,
	)

	c.nodePools.mutex.Lock()
	defer c.nodePools.mutex.Unlock()

	if len(c.nodePools.watchers) > 0 {
		for _, watcher := range c.nodePools.watchers {
			event := watcher.newEvent(key, nodePool)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processExternalAuthDocument(ctx context.Context, document json.RawMessage) error {
	var externalAuthDocument database.ExternalAuth

	err := json.Unmarshal(document, &externalAuthDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	externalAuth, err := database.CosmosToInternalExternalAuth(&externalAuthDocument)
	if err != nil {
		return err
	}

	key := toKey(externalAuth.ID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", externalAuth.ID.String(),
		"subscription_id", externalAuth.ID.SubscriptionID,
		"resource_group", externalAuth.ID.ResourceGroupName,
		"resource_name", externalAuth.ID.Name,
		"hcp_cluster_name", externalAuth.ID.Parent.Name,
	)

	c.externalAuths.mutex.Lock()
	defer c.externalAuths.mutex.Unlock()

	if len(c.externalAuths.watchers) > 0 {
		for _, watcher := range c.externalAuths.watchers {
			event := watcher.newEvent(key, externalAuth)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processServiceProviderClusterDocument(ctx context.Context, document json.RawMessage) error {
	var serviceProviderClusterDocument database.GenericDocument[api.ServiceProviderCluster]

	err := json.Unmarshal(document, &serviceProviderClusterDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	serviceProviderCluster, err := database.CosmosGenericToInternal(&serviceProviderClusterDocument)
	if err != nil {
		return err
	}

	key := toKey(&serviceProviderCluster.ResourceID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", serviceProviderCluster.ResourceID.String(),
		"subscription_id", serviceProviderCluster.ResourceID.SubscriptionID,
		"resource_group", serviceProviderCluster.ResourceID.ResourceGroupName,
		"resource_name", serviceProviderCluster.ResourceID.Name,
		"hcp_cluster_name", serviceProviderCluster.ResourceID.Parent.Name,
	)

	c.serviceProviderClusters.mutex.Lock()
	defer c.serviceProviderClusters.mutex.Unlock()

	if len(c.serviceProviderClusters.watchers) > 0 {
		for _, watcher := range c.serviceProviderClusters.watchers {
			event := watcher.newEvent(key, serviceProviderCluster)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processOperationDocument(ctx context.Context, document json.RawMessage) error {
	var operationDocument database.GenericDocument[api.Operation]

	err := json.Unmarshal(document, &operationDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	operation := &operationDocument.Content

	hcpClusterName := ""
	switch {
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		hcpClusterName = operation.ExternalID.Name
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		hcpClusterName = operation.ExternalID.Parent.Name
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		hcpClusterName = operation.ExternalID.Parent.Name
	}

	key := toKey(operation.ResourceID)
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource_id", operation.ResourceID.String(),
		"operation", operation.Request,
		"status", operation.Status,
		"subscription_id", operation.ResourceID.SubscriptionID,
		"resource_group", operation.ExternalID.ResourceGroupName,
		"resource_name", operation.ExternalID.Name,
		"hcp_cluster_name", hcpClusterName,
	)

	// Disregard operations with a terminal status.
	// Backend only cares about "active" operations.
	if operation.Status.IsTerminal() {
		logger.Info("skipped change feed event for terminal operation")
		return nil
	}

	c.operations.mutex.Lock()
	defer c.operations.mutex.Unlock()

	if len(c.operations.watchers) > 0 {
		for _, watcher := range c.operations.watchers {
			event := watcher.newEvent(key, operation)
			logger.Info(string(event.Type))
			watcher.result <- event
		}
	} else {
		logger.Info("dropped change feed event")
	}

	return nil
}

func (c *CosmosDBListWatch) processDocument(ctx context.Context, document json.RawMessage) error {
	var typedDocument database.TypedDocument

	// Unmarshal to a TypedDocument to peek at the resource type.
	err := json.Unmarshal(document, &typedDocument)
	if err != nil {
		return utils.TrackError(err)
	}

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(
		"resource_type", typedDocument.ResourceType,
		"document_ts", time.Unix(int64(typedDocument.CosmosTimestamp), 0).UTC().Format(time.RFC3339Nano))
	ctx = utils.ContextWithLogger(ctx, logger)

	switch {
	case strings.EqualFold(typedDocument.ResourceType, azcorearm.SubscriptionResourceType.String()):
		err = c.processSubscriptionDocument(ctx, document)
	case strings.EqualFold(typedDocument.ResourceType, api.ClusterResourceType.String()):
		err = c.processClusterDocument(ctx, document)
	case strings.EqualFold(typedDocument.ResourceType, api.NodePoolResourceType.String()):
		err = c.processNodePoolDocument(ctx, document)
	case strings.EqualFold(typedDocument.ResourceType, api.ExternalAuthResourceType.String()):
		err = c.processExternalAuthDocument(ctx, document)
	case strings.EqualFold(typedDocument.ResourceType, api.ServiceProviderClusterResourceType.String()):
		err = c.processServiceProviderClusterDocument(ctx, document)
	case strings.EqualFold(typedDocument.ResourceType, api.OperationStatusResourceType.String()):
		err = c.processOperationDocument(ctx, document)
	default:
		logger.Info("dropped change feed event")
	}

	return err
}

func (c *CosmosDBListWatch) readFeedRange(ctx context.Context, feedRange azcosmos.FeedRange) error {
	var changeFeedStatus int

	for changeFeedStatus != http.StatusNotModified {
		options := &azcosmos.ChangeFeedOptions{
			StartFrom: api.Ptr(c.startFrom),
		}

		if continuation, ok := c.continuationTokens.Load(feedRange); ok {
			// Continue from a previous read of this feed range.
			options.Continuation = api.Ptr(continuation.(string))
		} else {
			// First read for this feed range.
			options.FeedRange = api.Ptr(feedRange)
		}

		response, err := c.cosmosClient.GetResourcesChangeFeed(ctx, options)
		if err != nil {
			return utils.TrackError(err)
		}

		changeFeedStatus = response.RawResponse.StatusCode

		if changeFeedStatus == http.StatusOK {
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

func (c *CosmosDBListWatch) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	resourcesFeedRanges := c.cosmosClient.GetResourcesFeedRanges()

	// Initialize the workqueue with feed ranges.
	for _, feedRange := range resourcesFeedRanges {
		c.queue.Add(feedRange)
	}

	// Start a worker for each feed range.
	for i := 0; i < len(resourcesFeedRanges); i++ {
		go func() {
			for c.processNextWorkItem(ctx) {
			}
		}()
	}

	<-ctx.Done()
	c.queue.ShutDown()
	logger.Info("Shutting down")
}

func (c *CosmosDBListWatch) processNextWorkItem(ctx context.Context) bool {
	feedRange, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(feedRange)

	err := c.readFeedRange(ctx, feedRange)
	if err == nil {
		c.queue.Forget(feedRange)
		c.queue.AddAfter(feedRange, feedRangePollInterval)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", feedRange)

	c.queue.AddRateLimited(feedRange)

	return true
}
