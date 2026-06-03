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

package informers

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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

const feedRangePollInterval = 1 * time.Second

type ChangeFeedListWatcher[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	lock sync.Mutex

	desiredResourceTypes []azcorearm.ResourceType
	relistDuration       time.Duration
	clock                utilsclock.Clock
	globalLister         database.GlobalLister[InternalAPIType]
	cosmosClient         database.ResourcesDBClient

	currentWatcher *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]
}

func NewChangeFeedListWatcher[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	desiredResourceTypes []azcorearm.ResourceType, clock utilsclock.Clock, globalLister database.GlobalLister[InternalAPIType], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	return &ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		desiredResourceTypes: desiredResourceTypes,
		clock:                clock,
		globalLister:         globalLister,
		cosmosClient:         cosmosClient,
		relistDuration:       relistDuration,
	}
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	logger := utils.LoggerFromContext(ctx)
	logger.Info("listing clusters")
	defer logger.Info("finished listing clusters")

	// We create and start the watch before we do the list so that we won't miss any changefeed events due to a gap between
	// the end of the list and the start of the watch.
	// To avoid the problem of the changefeed providing the watch with stale information, the changefeed consumer only delivers
	// items that have a larger instanceVersion.

	prevFeedWatcher := c.currentWatcher
	c.currentWatcher = nil
	if prevFeedWatcher != nil {
		prevFeedWatcher.Stop()
	}

	c.currentWatcher = newChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType](c.desiredResourceTypes, c.clock, c.cosmosClient, c.clock.Now(), c.relistDuration)
	go c.currentWatcher.Run(ctx)

	resourceIDToInstanceVersion := &sync.Map{}

	iter, err := c.globalLister.List(ctx, nil)
	if err != nil {
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
		return nil, err
	}

	c.currentWatcher.beginDeliveryToWatcher(resourceIDToInstanceVersion)

	return list, nil
}

func (c *ChangeFeedListWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Watch(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
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

type ChangeFeedWatcher[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	desiredResourceTypes []azcorearm.ResourceType
	maxWatchDuration     time.Duration
	clock                utilsclock.Clock
	cosmosClient         database.ResourcesDBClient
	startFrom            time.Time

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

	result chan watch.Event
	done   chan struct{}
	// finished closes after Run and all of its child goroutines have fully
	// returned (including their deferred logging). Callers that need to be
	// sure no further work — especially logging through a test-bound logger
	// — will happen should wait on this before tearing down.
	finished chan struct{}
}

func newChangeFeedWatcher[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	desiredResourceTypes []azcorearm.ResourceType, clock utilsclock.Clock, cosmosClient database.ResourcesDBClient, startFrom time.Time, maxWatchDuration time.Duration) *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	return &ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		desiredResourceTypes:        desiredResourceTypes,
		maxWatchDuration:            maxWatchDuration,
		clock:                       clock,
		cosmosClient:                cosmosClient,
		startFrom:                   startFrom.Add(-2 * time.Second), // go back in time just a little bit so we collect everything
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
	logger.Info("starting change feed watcher")
	defer logger.Info("finished change feed watcher")

	var wg sync.WaitGroup
	defer wg.Wait()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("finished"))

	resourcesFeedRanges := c.cosmosClient.GetResourcesFeedRanges()

	// Initialize the workqueue with feed ranges.
	for _, feedRange := range resourcesFeedRanges {
		logger := utils.LoggerFromContext(ctx)
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
		case <-c.clock.After(c.maxWatchDuration):
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
			c.Stop()
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

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) processDocument(ctx context.Context, document json.RawMessage) error {
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

	objAsTypedDocument := &database.TypedDocument{}
	if err := json.Unmarshal(document, objAsTypedDocument); err != nil {
		return utils.TrackError(err)
	}
	matchesDesiredType := false
	for _, desiredResourceType := range c.desiredResourceTypes {
		if armhelpers.ResourceTypeStringEqual(objAsTypedDocument.ResourceType, desiredResourceType) {
			matchesDesiredType = true
			break
		}
	}
	if !matchesDesiredType {
		return nil
	}
	if objAsTypedDocument.ResourceID == nil {
		return utils.TrackError(fmt.Errorf("missing resourceID"))
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(document, &cosmosObj); err != nil {
		return utils.TrackError(err)
	}
	var internalObj InternalAPITypePointer
	var err error
	internalObj, err = database.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
	if err != nil {
		return utils.TrackError(err)
	}

	canonicalResourceID := strings.ToLower(internalObj.GetResourceID().String())
	initialInstanceVersion, objPreviouslySeen := c.resourceIDToInstanceVersion.Load(canonicalResourceID)
	if objPreviouslySeen && initialInstanceVersion.(int64) >= internalObj.GetInstanceVersion() {
		logger.Info("skipping document", "resourceID", canonicalResourceID, "instanceVersion", internalObj.GetInstanceVersion(), "initialInstanceVersion", initialInstanceVersion)
		return nil
	}
	c.resourceIDToInstanceVersion.Store(canonicalResourceID, internalObj.GetInstanceVersion())

	watchEvent := watch.Event{
		Object: any(internalObj).(runtime.Object),
	}
	if objPreviouslySeen {
		watchEvent.Type = watch.Modified
	} else {
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

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Stop() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) ResultChan() <-chan watch.Event {
	return c.result
}

// Finished returns a channel that is closed once Run and all of its child
// goroutines have fully exited. It is safe to call before, during, or after
// Run, and Stop must be invoked separately to actually trigger shutdown.
func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Finished() <-chan struct{} {
	return c.finished
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) runReadFeedRangeFn(feedRange azcosmos.FeedRange) func(ctx context.Context) {
	return func(ctx context.Context) {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("starting reading feed range")
		defer logger.Info("finished reading feed range")

		err := c.readFeedRange(ctx, feedRange)
		if err != nil {
			logger := utils.LoggerFromContext(ctx)
			logger.Error(err, "error reading feed range")
		}
	}
}

func (c *ChangeFeedWatcher[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) readFeedRange(ctx context.Context, feedRange azcosmos.FeedRange) error {
	logger := utils.LoggerFromContext(ctx)

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

		logger.Info("reading feed range", "options", options)
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
