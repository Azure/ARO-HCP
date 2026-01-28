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

package listers

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type SubscriptionLister BasicReader[arm.Subscription]

type BasicReader[InternalAPIType any] interface {
	HasSynced() bool
	// Get returns the item associated with the given name. If the item is not
	// found, nil is returned along with an azcore.ResponseError with StatusCode 404.
	Get(ctx context.Context, name string) (*InternalAPIType, error)
	List(ctx context.Context) ([]*InternalAPIType, error)
}

type BasicReaderMaintainer[InternalAPIType any] interface {
	ReplaceCache(ctx context.Context, delegate BasicReader[InternalAPIType])

	BasicReader[InternalAPIType]
}

// readOnlyContentLister is a lister that stores a list of items and a map of items by name.
// The list is used for ordered iteration, while the map provides fast lookup by name.
//
// This type assumes that for each item in items, if the item has name N (as determined by the caller
// when constructing itemsByName), then itemsByName[N] must point to that same item.
// In other words, itemsByName is an index into items, not a separate collection.
type readOnlyContentLister[InternalAPIType any] struct {
	items       []*InternalAPIType
	itemsByName map[string]*InternalAPIType
}

// NewReadOnlyContentLister creates a new readOnlyContentLister with the given items and itemsByName.
// The items and itemsByName must be consistent, i.e. for each item in items, if the item has name N (as determined by the caller
// when constructing itemsByName), then itemsByName[N] must point to that same item.
// In other words, itemsByName is an index into items, not a separate collection.
func NewReadOnlyContentLister[InternalAPIType any](items []*InternalAPIType, itemsByName map[string]*InternalAPIType) BasicReader[InternalAPIType] {
	return &readOnlyContentLister[InternalAPIType]{items: items, itemsByName: itemsByName}
}

func (r *readOnlyContentLister[InternalAPIType]) HasSynced() bool {
	return true
}

func (r *readOnlyContentLister[InternalAPIType]) Get(ctx context.Context, name string) (*InternalAPIType, error) {
	item, ok := r.itemsByName[name]
	if !ok {
		return nil, &azcore.ResponseError{
			ErrorCode:  http.StatusText(http.StatusNotFound),
			StatusCode: http.StatusNotFound,
		}
	}
	return item, nil
}

func (r *readOnlyContentLister[InternalAPIType]) List(ctx context.Context) ([]*InternalAPIType, error) {
	return r.items, nil
}

var _ BasicReader[any] = &readOnlyContentLister[any]{}

// threadSafeAtomicLister is a lister that stores an atomic value to indicate its state.  This style of lister is relatively
// expensive to maintain compared to something based on calculating diffs, but it is very easy to adapt from "list all of these"
// to "store the list of these".  Until we integrate a watch-style, the cost from cosmos is the same
type threadSafeAtomicLister[InternalAPIType any] struct {
	// delegate is updated when the contents change
	delegate  atomic.Value
	hasSynced atomic.Bool
}

func NewThreadSafeAtomicLister[InternalAPIType any]() BasicReaderMaintainer[InternalAPIType] {
	return &threadSafeAtomicLister[InternalAPIType]{}
}

func (r *threadSafeAtomicLister[InternalAPIType]) HasSynced() bool {
	return r.hasSynced.Load()
}

func (r *threadSafeAtomicLister[InternalAPIType]) ReplaceCache(ctx context.Context, delegate BasicReader[InternalAPIType]) {
	r.delegate.Store(delegate)
	r.hasSynced.Store(true)
}

func (t *threadSafeAtomicLister[InternalAPIType]) Get(ctx context.Context, name string) (*InternalAPIType, error) {
	ret := t.delegate.Load()
	if ret == nil {
		return nil, fmt.Errorf("lister not initialized")
	}

	return ret.(BasicReader[InternalAPIType]).Get(ctx, name)
}

func (t *threadSafeAtomicLister[InternalAPIType]) List(ctx context.Context) ([]*InternalAPIType, error) {
	ret := t.delegate.Load()
	if ret == nil {
		return nil, fmt.Errorf("lister not initialized")
	}

	return ret.(BasicReader[InternalAPIType]).List(ctx)
}

var _ BasicReader[any] = &threadSafeAtomicLister[any]{}
