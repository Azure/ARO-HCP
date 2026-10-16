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
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var operationResourceTypes = []azcorearm.ResourceType{coreapi.OperationStatusResourceType}

type recordingChangeFeedClient struct {
	onReadFeedRanges func(context.Context)
	onReadChangeFeed func(context.Context)
}

func (c recordingChangeFeedClient) ReadFeedRanges(ctx context.Context, _ *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	if c.onReadFeedRanges != nil {
		c.onReadFeedRanges(ctx)
	}
	return nil, nil
}

func (c recordingChangeFeedClient) ReadChangeFeed(ctx context.Context, _ *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	if c.onReadChangeFeed != nil {
		c.onReadChangeFeed(ctx)
	}
	return azcosmos.ChangeFeedResponse{
		Response: azcosmos.Response{RawResponse: &http.Response{StatusCode: http.StatusNotModified}},
	}, nil
}

type recordingLister struct {
	onList func(context.Context)
}

func (l recordingLister) List(ctx context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Operation], error) {
	l.onList(ctx)
	return emptyIterator{}, nil
}

type emptyIterator struct{}

func (emptyIterator) Items(context.Context) cosmosstorageutils.DBClientIteratorItem[coreapi.Operation] {
	return func(func(string, *coreapi.Operation) bool) {}
}
func (emptyIterator) GetContinuationToken() string { return "" }
func (emptyIterator) GetError() error              { return nil }

func requireAttributed(t *testing.T, ctx context.Context, stage string) {
	t.Helper()
	require.NotNil(t, ctx, "%s was never called", stage)
	name, ok := InformerNameFromContext(ctx)
	require.True(t, ok, "%s lacks informer attribution", stage)
	require.Equal(t, "Operations", name, stage)
}

// TestChangeFeedListWatcherPropagatesAttribution calls the three real,
// production entry points that read from Cosmos DB directly and
// synchronously — no goroutines, channels, or timeouts — proving none of
// them drop the attributed context:
//
//   - list: ChangeFeedListWatcher.List calls globalLister.List synchronously.
//   - discovery: ChangeFeedWatcher.Run calls ReadFeedRanges before checking
//     ctx cancellation, so calling Run with an already-canceled context
//     exercises discovery and returns as soon as its (already-exiting)
//     child goroutines join, with no wait needed.
//   - poll: readFeedRange is the plain per-feed-range function the real
//     watcher schedules on a ticker; called directly it runs exactly once
//     because the fake client reports StatusNotModified immediately.
func TestChangeFeedListWatcherPropagatesAttribution(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(ContextWithInformerName(context.Background(), "Operations"), logr.Discard())

	t.Run("list", func(t *testing.T) {
		var listCtx context.Context
		lw := NewChangeFeedListWatcher[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](
			operationResourceTypes, utilsclock.RealClock{},
			recordingLister{onList: func(c context.Context) { listCtx = c }},
			recordingChangeFeedClient{},
			time.Hour, "",
		)
		_, err := lw.List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		lw.currentWatcher.Stop() // join the background watcher List() started; not under test here
		requireAttributed(t, listCtx, "list")
	})

	t.Run("discovery", func(t *testing.T) {
		var discoveryCtx context.Context
		watcher := newChangeFeedWatcher[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](
			operationResourceTypes, utilsclock.RealClock{},
			recordingChangeFeedClient{onReadFeedRanges: func(c context.Context) { discoveryCtx = c }},
			time.Now(), time.Hour, nil, "", nil,
		)
		runCtx, cancel := context.WithCancel(ctx)
		cancel()
		watcher.Run(runCtx)
		requireAttributed(t, discoveryCtx, "discovery")
	})

	t.Run("poll", func(t *testing.T) {
		var pollCtx context.Context
		watcher := newChangeFeedWatcher[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](
			operationResourceTypes, utilsclock.RealClock{},
			recordingChangeFeedClient{onReadChangeFeed: func(c context.Context) { pollCtx = c }},
			time.Now(), time.Hour, nil, "", nil,
		)
		err := watcher.readFeedRange(ctx, azcosmos.FeedRange{MinInclusive: "", MaxExclusive: "FF"})
		require.NoError(t, err)
		requireAttributed(t, pollCtx, "poll")
	})
}
