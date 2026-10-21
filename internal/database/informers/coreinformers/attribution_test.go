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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// staticLister reports a single empty page and records the context it
// observed. List/discovery/poll attribution propagation is covered once,
// generically, in informerutils — this only needs to know what context each
// real production constructor's List call actually receives.
type staticLister[T any] struct {
	calls chan context.Context
}

func (l staticLister[T]) List(ctx context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[T], error) {
	select {
	case l.calls <- ctx:
	case <-ctx.Done():
	}
	return emptyIterator[T]{}, nil
}

type emptyIterator[T any] struct{}

func (emptyIterator[T]) Items(context.Context) cosmosstorageutils.DBClientIteratorItem[T] {
	return func(func(string, *T) bool) {}
}
func (emptyIterator[T]) GetContinuationToken() string { return "" }
func (emptyIterator[T]) GetError() error              { return nil }

// noRangesChangeFeedClient reports zero feed ranges so change-feed informers
// spawn no feed-range reader goroutines; only the informer's List wiring is
// under test here.
type noRangesChangeFeedClient struct{}

func (noRangesChangeFeedClient) ReadFeedRanges(context.Context, *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return nil, nil
}

func (noRangesChangeFeedClient) ReadChangeFeed(context.Context, *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	return azcosmos.ChangeFeedResponse{}, nil // never reached: no feed ranges means no reader calls this
}

// checkInformerWiresAttribution proves a real production informer
// constructor's List call carries its own InformerName and the ambient
// controller name. That the shared ListWatchWithoutWatchListSemantics
// wrapper itself takes precedence over an already-attributed parent and
// never mutates it is proven once, generically, in list_watch_test.go; this
// only needs to catch a constructor-specific bug (e.g. a ListWithContextFunc
// closure that drops the ctx it was given) in the full real chain.
func checkInformerWiresAttribution[T any](t *testing.T, expectedName string, create func(cosmosstorageutils.GlobalLister[T], cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	ctx = utils.ContextWithControllerName(ctx, "ConsumingController")
	defer cancel()

	calls := make(chan context.Context, 1)
	informer := create(staticLister[T]{calls: calls}, noRangesChangeFeedClient{})
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
			t.Error("informer did not stop after cancellation")
		}
	})

	select {
	case listCtx := <-calls:
		name, ok := informerutils.InformerNameFromContext(listCtx)
		require.True(t, ok, "list lacks informer attribution")
		require.Equal(t, expectedName, name)
		controller, ok := utils.ControllerNameFromContext(listCtx)
		require.True(t, ok, "list discarded parent controller attribution")
		require.Equal(t, "ConsumingController", controller)
	case <-ctx.Done():
		t.Fatal("timed out waiting for the informer's first list call")
	}

}

func TestOperationInformerAttribution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func(cosmosstorageutils.GlobalLister[coreapi.Operation], cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer
	}{
		{name: "ActiveOperations", create: func(lister cosmosstorageutils.GlobalLister[coreapi.Operation], feed cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewActiveOperationInformerWithRelistDuration(lister, feed, time.Hour)
		}},
		{name: "AllOperations", create: func(lister cosmosstorageutils.GlobalLister[coreapi.Operation], feed cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewOperationInformerWithRelistDuration(lister, feed, time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkInformerWiresAttribution(t, tc.name, tc.create)
		})
	}
}

func TestNonChangeFeedInformerAttribution(t *testing.T) {
	t.Run("BillingDocs", func(t *testing.T) {
		t.Parallel()
		checkInformerWiresAttribution(t, "BillingDocs", func(lister cosmosstorageutils.GlobalLister[billingcosmosstorage.BillingDocument], _ cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewBillingInformerWithRelistDuration(lister, time.Hour)
		})
	})
	t.Run("ManagementClusterContents", func(t *testing.T) {
		t.Parallel()
		checkInformerWiresAttribution(t, "ManagementClusterContents", func(lister cosmosstorageutils.GlobalLister[coreapi.ManagementClusterContent], _ cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
			return NewManagementClusterContentInformerWithRelistDuration(lister, time.Hour)
		})
	})
}
