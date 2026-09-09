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

package informerutils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// changeFeedItemObjectMetadata builds the objectMetadata attached to change-feed item logs so they
// ingest into cosmosResourceSnapshots with full resource identity. resourceID is the document's own
// ResourceID, from which ObjectMetadataForResourceID already derives the enclosing cluster.
// Operations are special: their own ResourceID is subscription/location-scoped and identifies
// neither a resource group nor a cluster, so their metadata is derived from the operation's
// ExternalID via the shared cosmosstorageutils.ObjectMetadataForOperation helper (also used by the
// datadump path) to keep the two paths in sync. The caller mirrors the returned ClusterResourceID
// into the flat hcp_cluster_name / cluster_id log fields.
func changeFeedItemObjectMetadata(cosmosContainerName string, internalObj any, resourceID *azcorearm.ResourceID) metadataapi.ObjectMetadata {
	if operation, ok := internalObj.(*coreapi.Operation); ok {
		objectMetadata := cosmosstorageutils.ObjectMetadataForOperation(operation)
		return objectMetadata
	}
	return metadataapi.ObjectMetadataForResourceID(cosmosContainerName, resourceID)
}

const feedRangePollInterval = 1 * time.Second

type ShouldDeliverFunc[InternalAPITypePointer any] func(obj InternalAPITypePointer) bool

type ChangeFeedListWatcher[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	lock sync.Mutex

	desiredResourceTypes []azcorearm.ResourceType
	relistDuration       time.Duration
	clock                utilsclock.Clock
	globalLister         cosmosstorageutils.GlobalLister[InternalAPIType]
	changeFeedClient     cosmosstorageutils.ChangeFeedClient
	shouldDeliverItemFn  ShouldDeliverFunc[InternalAPITypePointer]
	// jitterFn spreads each watch's expiry (see defaultJitter); defaulted to
	// defaultJitter in NewChangeFeedListWatcher. It is intentionally not
	// externally configurable — same-package tests may set it directly for
	// deterministic timing.
	jitterFn JitterFunc
	// cosmosContainerName is the Cosmos container label emitted as the objectMetadata.cosmosContainer
	// of every delivered/skipped change-feed item so the item is ingested into
	// cosmosResourceSnapshots with full resource metadata. It is required at construction; an empty
	// value suppresses objectMetadata emission.
	cosmosContainerName string

	currentWatcher *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]
}

// NewChangeFeedListWatcher builds a change-feed-backed ListWatcher. cosmosContainerName is the
// Cosmos container label recorded on every emitted cosmosResourceSnapshots item (e.g. "resources",
// "fleet", "kubeApplier"); pass "" only for informers whose items should not carry objectMetadata.
func NewChangeFeedListWatcher[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	desiredResourceTypes []azcorearm.ResourceType, clock utilsclock.Clock, globalLister cosmosstorageutils.GlobalLister[InternalAPIType], changeFeedClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration, cosmosContainerName string) *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	return &ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		desiredResourceTypes: desiredResourceTypes,
		clock:                clock,
		globalLister:         globalLister,
		changeFeedClient:     changeFeedClient,
		relistDuration:       relistDuration,
		cosmosContainerName:  cosmosContainerName,
		jitterFn:             defaultJitter,
	}
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) WithShouldDeliverItemFn(shouldDeliverItemFn ShouldDeliverFunc[InternalAPITypePointer]) *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	c.shouldDeliverItemFn = shouldDeliverItemFn
	return c
}

func waitForWatcher[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](ctx context.Context, clock utilsclock.Clock, watcher *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) error {
	logger := utils.LoggerFromContext(ctx)
	for {
		select {
		case <-watcher.Finished():
			return nil
		case <-ctx.Done():
			return fmt.Errorf("failed to stop previous watcher before timeout: %w", ctx.Err())
		case <-clock.After(5 * time.Second):
			logger.Info("waiting for previous watcher to stop")
		}
	}
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddResourceTypes(c.desiredResourceTypes...)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	logger.Info("listing")
	defer logger.Info("finished listing")

	// We create and start the watch before we do the list so that we won't miss any changefeed events due to a gap between
	// the end of the list and the start of the watch.
	// To avoid the problem of the changefeed providing the watch with stale information, the changefeed consumer only delivers
	// items that have a larger instanceVersion.

	prevFeedWatcher := c.currentWatcher
	c.currentWatcher = nil
	if prevFeedWatcher != nil {
		prevFeedWatcher.Stop()
		if err := waitForWatcher(ctx, c.clock, prevFeedWatcher); err != nil {
			logger.Error(err, "failed to wait for previous watcher to stop, continuing")
		}
	}

	c.currentWatcher = newChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType](c.desiredResourceTypes, c.clock, c.changeFeedClient, c.clock.Now(), c.relistDuration, c.shouldDeliverItemFn, c.cosmosContainerName, c.jitterFn)
	go c.currentWatcher.Run(ctx)

	resourceIDToInstanceVersion := &sync.Map{}

	iter, err := c.globalLister.List(ctx, nil)
	if err != nil {
		c.currentWatcher.Stop()
		if err := waitForWatcher(ctx, c.clock, c.currentWatcher); err != nil {
			logger.Error(err, "failed to wait for current watcher to stop, continuing")
		}
		c.currentWatcher = nil

		return nil, err
	}

	list := &metav1.List{
		ListMeta: metav1.ListMeta{
			ResourceVersion: "0",
		},
		Items: []runtime.RawExtension{},
	}
	for _, currItemObj := range iter.Items(ctx) {
		currObj := InternalAPITypePointer(currItemObj)
		resourceIDToInstanceVersion.Store(strings.ToLower(currObj.GetResourceID().String()), currObj.GetInstanceVersion())

		list.Items = append(list.Items,
			runtime.RawExtension{
				Object: any(currObj).(runtime.Object),
			})
	}
	if err := iter.GetError(); err != nil {
		c.currentWatcher.Stop()
		if err := waitForWatcher(ctx, c.clock, c.currentWatcher); err != nil {
			logger.Error(err, "failed to wait for current watcher to stop, continuing")
		}
		c.currentWatcher = nil

		return nil, err
	}

	c.currentWatcher.beginDeliveryToWatcher(resourceIDToInstanceVersion)

	return list, nil
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Watch(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("watching")
	defer logger.Info("returned watcher")

	c.lock.Lock()
	defer c.lock.Unlock()

	if c.currentWatcher != nil {
		select {
		case <-c.currentWatcher.done:
			c.currentWatcher = nil
			return nil, fmt.Errorf("current watcher done and removed")
		default:
			return c.currentWatcher, nil
		}
	}

	return nil, fmt.Errorf("no current watcher")
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) ToListWatch() *cache.ListWatch {
	return &cache.ListWatch{
		ListWithContextFunc:  c.List,
		WatchFuncWithContext: c.Watch,
	}
}

// Stop stops the currently-running ChangeFeedWatcher (if any) and blocks
// until its Run goroutine and every child goroutine it spawned have fully
// returned. Test cleanup paths that share a logger with the underlying
// *testing.T must wait here before letting the test function return — the
// test logger panics if it is invoked after the test completes.
func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Stop() {
	c.lock.Lock()
	watcher := c.currentWatcher
	c.currentWatcher = nil
	c.lock.Unlock()
	if watcher == nil {
		return
	}
	watcher.Stop()
	<-watcher.Finished()
}

type ChangeFeedWatcher[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	desiredResourceTypes []azcorearm.ResourceType
	maxWatchDuration     time.Duration
	// jitterFn spreads maxWatchDuration per watch (see defaultJitter) so
	// informers sharing a relist duration do not relist in lockstep.
	jitterFn            JitterFunc
	clock               utilsclock.Clock
	changeFeedClient    cosmosstorageutils.ChangeFeedClient
	startFrom           time.Time
	shouldDeliverItemFn ShouldDeliverFunc[InternalAPITypePointer]
	// cosmosContainerName, when set, is emitted as objectMetadata.cosmosContainer on every
	// change-feed item log so the item is ingested into cosmosResourceSnapshots with full
	// resource metadata. Empty means "do not emit objectMetadata".
	cosmosContainerName string

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

	beginDelivery               chan struct{}
	resourceIDToInstanceVersion *sync.Map

	result   chan watch.Event
	done     chan struct{}
	stopOnce sync.Once
	// finished closes after Run and all of its child goroutines have fully
	// returned (including their deferred logging). Callers that need to be
	// sure no further work — especially logging through a test-bound logger
	// — will happen should wait on this before tearing down.
	finished chan struct{}
}

func newChangeFeedWatcher[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	desiredResourceTypes []azcorearm.ResourceType, clock utilsclock.Clock, changeFeedClient cosmosstorageutils.ChangeFeedClient, startFrom time.Time, maxWatchDuration time.Duration, shouldDeliverFn ShouldDeliverFunc[InternalAPITypePointer], cosmosContainerName string, jitterFn JitterFunc) *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	if jitterFn == nil {
		jitterFn = defaultJitter
	}
	return &ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		desiredResourceTypes:        desiredResourceTypes,
		maxWatchDuration:            maxWatchDuration,
		jitterFn:                    jitterFn,
		clock:                       clock,
		changeFeedClient:            changeFeedClient,
		startFrom:                   startFrom.Add(-2 * time.Second), // go back in time just a little bit so we collect everything
		shouldDeliverItemFn:         shouldDeliverFn,
		cosmosContainerName:         cosmosContainerName,
		continuationTokens:          sync.Map{},
		beginDelivery:               make(chan struct{}),
		resourceIDToInstanceVersion: nil,
		result:                      make(chan watch.Event, 100),
		done:                        make(chan struct{}),
		finished:                    make(chan struct{}),
	}
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()
	// Defers fire LIFO. We want, on return:
	//   1. cancel — signal child goroutines to wind down
	//   2. wg.Wait — block until every child goroutine has fully exited,
	//      including any deferred logging they emit
	//   3. logger.Info("finished change feed watcher") — final log line
	//   4. close(c.finished) — only now is it safe for waiters to assume
	//      no further logging will happen via ctx's logger
	defer close(c.finished)

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.
		AddResourceTypes(c.desiredResourceTypes...)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	logger.Info("starting change feed watchers")
	defer logger.Info("finished change feed watchers")

	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		close(c.result)
	}()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("finished"))

	options := &azcosmos.FeedRangesOptions{}
	feedRanges, err := c.changeFeedClient.ReadFeedRanges(ctx, options)
	if err != nil {
		retErr := utils.TrackError(err)
		utilruntime.HandleError(retErr)
		c.signalStop()
		cancel(retErr)
		return
	}

	// Initialize the workqueue with feed ranges.
	for _, feedRange := range feedRanges {
		localFeedRange := feedRange
		localCtx := utils.ContextWithLogger(ctx, logger.WithValues("feedRange", localFeedRange))

		wg.Add(1)
		go func() {
			defer wg.Done()
			wait.UntilWithContext(localCtx, c.runReadFeedRangeFn(localFeedRange), feedRangePollInterval)
		}()
	}

	wg.Add(1)
	go func(ctx context.Context) {
		defer utilruntime.HandleCrash()
		defer wg.Done()

		select {
		case <-ctx.Done():
			return
		// Jitter the max watch duration (see defaultJitter) so watchers sharing a
		// relist duration expire at slightly different times, preventing a
		// thundering-herd of simultaneous relists against Cosmos DB.
		case <-c.clock.After(c.jitterFn(c.maxWatchDuration)):
			// Signal to the consuming Reflector that the watch has
			// expired so it will relist. Without this the Reflector
			// just sees the result channel block and never reissues
			// List/Watch. Mirrors NewExpiringWatcher's behavior.
			select {
			case c.result <- watch.Event{
				Type: watch.Error,
				Object: &metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    http.StatusGone,
					Reason:  metav1.StatusReasonExpired,
					Message: "change feed watch expired",
				},
			}:
			case <-c.done:
			case <-ctx.Done():
			}
			c.signalStop()
			return
		}
	}(ctx)

	select {
	case <-c.done:
		cancel(fmt.Errorf("watch closed"))
	case <-ctx.Done():
	}
}

// TODO this breaks on the delete and recreate scenario. We need to add a true UUID.
func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) beginDeliveryToWatcher(resourceIDToInitialInstanceVersion *sync.Map) {
	c.resourceIDToInstanceVersion = resourceIDToInitialInstanceVersion
	close(c.beginDelivery)
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) processItem(ctx context.Context, item []byte) error {
	logger := utils.LoggerFromContext(ctx)
	ready := false
	for !ready {
		select {
		case <-c.done:
			return nil
		case <-ctx.Done():
			return nil
		case <-c.beginDelivery:
			ready = true
		case <-c.clock.After(5 * time.Second):
			logger.Info("waiting for beginDelivery")
		}
	}

	objAsTypedDocument := &cosmosstorageutils.TypedDocument{}
	if err := json.Unmarshal(item, objAsTypedDocument); err != nil {
		return utils.TrackError(err)
	}
	logger = logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(objAsTypedDocument.ResourceID)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	matchesDesiredType := false
	for _, desiredResourceType := range c.desiredResourceTypes {
		if metadataapi.ResourceTypeStringEqual(objAsTypedDocument.ResourceType, desiredResourceType) {
			matchesDesiredType = true
			break
		}
	}
	if !matchesDesiredType {
		return nil
	}

	if objAsTypedDocument.ResourceID == nil {
		// intentionally skipping malformed object
		utilruntime.HandleError(fmt.Errorf("missing resourceID for document ID: %q", objAsTypedDocument.ID))
		return nil
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(item, &cosmosObj); err != nil {
		return utils.TrackError(err)
	}
	var internalObj InternalAPITypePointer
	var err error
	internalObj, err = cosmosstorageutils.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
	if err != nil {
		return utils.TrackError(err)
	}

	// When a Cosmos container is configured, emit objectMetadata so this item is ingested into
	// cosmosResourceSnapshots with full resource identity, and derive the HCP cluster name for
	// documents (e.g. operations) whose own ResourceID can't provide it. See
	// changeFeedItemObjectMetadata for the details. This mirrors dump_data.go so change-feed-
	// sourced snapshots carry the same metadata columns as request-triggered dumps.
	if len(c.cosmosContainerName) > 0 {
		objectMetadata := changeFeedItemObjectMetadata(c.cosmosContainerName, any(internalObj), internalObj.GetResourceID())
		if objectMetadata.ClusterResourceID != "" {
			logger = logger.WithValues(utils.LogValues{}.AddHCPClusterName(objectMetadata.ClusterResourceID)...)
		}
		logger = logger.WithValues("objectMetadata", objectMetadata)
		ctx = utils.ContextWithLogger(ctx, logger)
	}

	canonicalResourceID := strings.ToLower(internalObj.GetResourceID().String())
	initialInstanceVersion, objPreviouslySeen := c.resourceIDToInstanceVersion.Load(canonicalResourceID)
	if objPreviouslySeen && initialInstanceVersion.(int64) >= internalObj.GetInstanceVersion() {
		logger.Info("skipping document", "instanceVersion", internalObj.GetInstanceVersion(), "initialInstanceVersion", initialInstanceVersion)
		return nil
	}

	loggedDocument := &cosmosstorageutils.TypedDocument{}
	*loggedDocument = *objAsTypedDocument
	if err := cosmosstorageutils.RedactTypedDocument(loggedDocument); err != nil {
		utilruntime.HandleError(utils.TrackError(err))
		loggedDocument = nil
	}

	objDeleted := false
	if objAsTypedDocument.DeletionTimestamp != nil {
		if objPreviouslySeen {
			objDeleted = true
		} else {
			logger.Info("skipping soft-deleted document not previously seen",
				"snapshotType", "cosmos",
				"content", loggedDocument)
			return nil
		}
	} else if c.shouldDeliverItemFn != nil && !c.shouldDeliverItemFn(internalObj) {
		if objPreviouslySeen {
			objDeleted = true
		} else {
			logger.Info("should not deliver document",
				"snapshotType", "cosmos",
				"content", loggedDocument)
			return nil
		}
	}

	logger.Info("delivering change feed item",
		"snapshotType", "cosmos",
		"content", loggedDocument,
	)
	if objDeleted {
		c.resourceIDToInstanceVersion.Delete(canonicalResourceID)
	} else {
		c.resourceIDToInstanceVersion.Store(canonicalResourceID, internalObj.GetInstanceVersion())
	}

	watchEvent := watch.Event{
		Object: any(internalObj).(runtime.Object),
	}
	switch {
	case objDeleted:
		watchEvent.Type = watch.Deleted
	case objPreviouslySeen:
		watchEvent.Type = watch.Modified
	default:
		watchEvent.Type = watch.Added
	}

	sent := false
	for !sent {
		select {
		case <-c.done:
			return nil
		case <-ctx.Done():
			return nil
		case c.result <- watchEvent:
			sent = true
		case <-c.clock.After(5 * time.Second):
			logger.Info("waiting to send")
		}
	}

	return nil
}

// Stop signals the watcher to shut down and blocks until Run and every child
// goroutine it spawned have fully exited (see Finished), including their
// deferred logging. The client-go Reflector calls Stop when it tears a watch
// down, so this join is what ties the watcher's lifetime to the informer's:
// when a watcher shares a logger with a *testing.T, its deferred shutdown
// logging must finish before the test returns, otherwise the test logger
// races/panics on a log emitted after the test has completed. Callers running
// on the Run goroutine (or on a goroutine that Run waits for) must use
// signalStop instead — blocking here would deadlock waiting on their own
// Finished channel.
func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Stop() {
	c.signalStop()
	<-c.finished
}

// signalStop triggers shutdown without waiting for it to complete. It is the
// non-blocking counterpart to Stop, safe to call from the Run goroutine and
// from the child goroutines that Run joins on.
func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) signalStop() {
	c.stopOnce.Do(func() {
		close(c.done)
	})
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) ResultChan() <-chan watch.Event {
	return c.result
}

// Finished returns a channel that is closed once Run and all of its child
// goroutines have fully exited. It is safe to call before, during, or after
// Run. Stop triggers shutdown and waits for this channel to close.
func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Finished() <-chan struct{} {
	return c.finished
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) runReadFeedRangeFn(feedRange azcosmos.FeedRange) func(ctx context.Context) {
	return func(ctx context.Context) {
		logger := utils.LoggerFromContext(ctx)
		logger.V(4).Info("starting reading feed range")
		defer logger.V(4).Info("finished reading feed range")

		err := c.readFeedRange(ctx, feedRange)
		if err != nil {
			logger.Error(err, "error reading feed range")
		}
	}
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) readFeedRange(ctx context.Context, feedRange azcosmos.FeedRange) error {
	logger := utils.LoggerFromContext(ctx)

	var changeFeedStatus int

	for changeFeedStatus != http.StatusNotModified {
		options := &azcosmos.ChangeFeedOptions{
			StartFrom: metadataapi.Ptr(c.startFrom),
		}

		if continuation, ok := c.continuationTokens.Load(feedRange); ok {
			// Continue from a previous read of this feed range.
			options.Continuation = metadataapi.Ptr(continuation.(string))
		} else {
			// First read for this feed range.
			options.FeedRange = metadataapi.Ptr(feedRange)
		}

		logger.V(4).Info("reading feed range", "options", options)
		response, err := c.changeFeedClient.ReadChangeFeed(ctx, options)
		if err != nil {
			return utils.TrackError(err)
		}

		changeFeedStatus = response.RawResponse.StatusCode

		if changeFeedStatus == http.StatusOK {
			for _, item := range response.Items {
				err = c.processItem(ctx, item)
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
