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
	"sync/atomic"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type SubscriptionLister BasicReader[arm.Subscription]

type BasicReader[InternalAPIType any] interface {
	HasSynced() bool
	Get(ctx context.Context, name string) (*InternalAPIType, error)
	List(ctx context.Context) ([]*InternalAPIType, error)
}

type BasicReaderMaintainer[InternalAPIType any] interface {
	ReplaceCache(ctx context.Context, delegate BasicReader[InternalAPIType])

	BasicReader[InternalAPIType]
}

type readOnlyContentLister[InternalAPIType any] struct {
	items []*InternalAPIType
}

func NewReadOnlyContentLister[InternalAPIType any](items []*InternalAPIType) BasicReader[InternalAPIType] {
	return &readOnlyContentLister[InternalAPIType]{items: items}
}

func (r *readOnlyContentLister[InternalAPIType]) HasSynced() bool {
	return true
}

func (r *readOnlyContentLister[InternalAPIType]) Get(ctx context.Context, name string) (*InternalAPIType, error) {
	//TODO implement me
	panic("implement me")
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
