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

package coreinformers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type attributionCall struct {
	stage string
	ctx   context.Context
}

type attributionRecorder struct {
	calls chan attributionCall
}

func (r *attributionRecorder) record(ctx context.Context, stage string) {
	select {
	case r.calls <- attributionCall{stage: stage, ctx: ctx}:
	case <-ctx.Done():
	}
}

func (r *attributionRecorder) ReadFeedRanges(ctx context.Context, _ *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	r.record(ctx, "discovery")
	return []azcosmos.FeedRange{{MinInclusive: "", MaxExclusive: "FF"}}, nil
}

func (r *attributionRecorder) ReadChangeFeed(ctx context.Context, _ *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	r.record(ctx, "poll")
	return azcosmos.ChangeFeedResponse{
		Response: azcosmos.Response{RawResponse: &http.Response{StatusCode: http.StatusNotModified}},
	}, nil
}

type attributionLister[T any] struct {
	recorder *attributionRecorder
}

func (l attributionLister[T]) List(ctx context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[T], error) {
	l.recorder.record(ctx, "list")
	page := 0
	pager := azruntime.NewPager(azruntime.PagingHandler[azcosmos.QueryItemsResponse]{
		More: func(response azcosmos.QueryItemsResponse) bool {
			return response.ContinuationToken != nil
		},
		Fetcher: func(ctx context.Context, _ *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
			l.recorder.record(ctx, "page")
			page++
			response := azcosmos.QueryItemsResponse{}
			if page == 1 {
				token := "next-page"
				response.ContinuationToken = &token
			}
			return response, nil
		},
	})
	return cosmosstorageutils.NewQueryResourcesIterator[T, cosmosstorageutils.GenericDocument[T]](pager), nil
}

// Running real informers exercises the client-go context-aware List/Watch
// boundary, including query pagination and the reflector's automatic relist.
func checkInformerAttribution[T any](t *testing.T, name string, changeFeed bool, create func(cosmosstorageutils.GlobalLister[T], cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	ctx = utils.ContextWithControllerName(ctx, "ConsumingController")
	ctx = cosmosmetrics.ContextWithInformerName(ctx, "ParentInformer")
	defer cancel()
	recorder := &attributionRecorder{calls: make(chan attributionCall, 32)}
	informer := create(attributionLister[T]{recorder: recorder}, recorder)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		informer.RunWithContext(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("informer did not stop after parent cancellation")
		}
	})

	want := map[string]int{"list": 2, "page": 4}
	if changeFeed {
		// The 1.5s expiry allows a second poll before discovery is repeated
		// for the relist. This covers both long-lived and replacement feeds.
		want["discovery"] = 2
		want["poll"] = 3
	}
	seen := map[string]int{}
	var observed []context.Context
	for len(want) > 0 {
		select {
		case call := <-recorder.calls:
			got, ok := cosmosmetrics.InformerNameFromContext(call.ctx)
			require.True(t, ok, "%s lacks informer attribution", call.stage)
			require.Equal(t, name, got, call.stage)
			controller, ok := utils.ControllerNameFromContext(call.ctx)
			require.True(t, ok, "%s discarded parent values", call.stage)
			require.Equal(t, "ConsumingController", controller)
			deadline, ok := call.ctx.Deadline()
			parentDeadline, _ := ctx.Deadline()
			require.True(t, ok, "%s discarded parent deadline", call.stage)
			require.Equal(t, parentDeadline, deadline)
			observed = append(observed, call.ctx)
			seen[call.stage]++
			if target, ok := want[call.stage]; ok && seen[call.stage] >= target {
				delete(want, call.stage)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for attributed initial list/feed/relist: remaining %v, observed %v", want, seen)
		}
	}

	parentName, ok := cosmosmetrics.InformerNameFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "ParentInformer", parentName, "informer mutated parent attribution")
	cancel()
	for _, child := range observed {
		require.ErrorIs(t, child.Err(), context.Canceled, "parent cancellation must reach lists and feed goroutines")
	}
}

func TestOperationInformerAttribution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func(cosmosstorageutils.GlobalLister[coreapi.Operation], cosmosstorageutils.ChangeFeedClient, time.Duration) cache.SharedIndexInformer
	}{
		{name: "ActiveOperations", create: NewActiveOperationInformerWithRelistDuration},
		{name: "AllOperations", create: NewOperationInformerWithRelistDuration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkInformerAttribution(t, tc.name, true, func(lister cosmosstorageutils.GlobalLister[coreapi.Operation], feed cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
				return tc.create(lister, feed, 1500*time.Millisecond)
			})
		})
	}
}

func TestNonChangeFeedInformerAttribution(t *testing.T) {
	// Production uses 30-second list-only watches. Shorten only the expiry so
	// the real reflector relist can be exercised without a 30-second test.
	t.Run("BillingDocs", func(t *testing.T) {
		checkInformerAttribution(t, "BillingDocs", false, func(lister cosmosstorageutils.GlobalLister[billingcosmosstorage.BillingDocument], _ cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewBillingInformerWithRelistDuration(lister, 10*time.Millisecond)
		})
	})
	t.Run("ManagementClusterContents", func(t *testing.T) {
		checkInformerAttribution(t, "ManagementClusterContents", false, func(lister cosmosstorageutils.GlobalLister[coreapi.ManagementClusterContent], _ cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewManagementClusterContentInformerWithRelistDuration(lister, 10*time.Millisecond)
		})
	})
}
