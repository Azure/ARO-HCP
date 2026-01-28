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
	"sync"
	"sync/atomic"

	"github.com/Azure/ARO-HCP/internal/api"
)

type OperationLister PerSubscriptionReader[api.Operation]

type PerSubscriptionReader[InternalAPIType any] interface {
	Subscription(subscriptionName string) BasicReader[InternalAPIType]
}

type PerSubscriptionMaintainer[InternalAPIType any] interface {
	ConfirmSynced()
	SetSubscriptionValue(subscriptionName string, reader BasicReader[InternalAPIType])

	PerSubscriptionReader[InternalAPIType]
}

type PerResourceGroupReader[InternalAPIType any] interface {
	ResourceGroup(subscriptionName, resourceGroupName string) BasicReader[InternalAPIType]
}

type PerResourceGroupMaintainer[InternalAPIType any] interface {
	ConfirmSynced()
	SetResourceGroupValue(subscriptionName, resourceGroupName string, reader BasicReader[InternalAPIType])

	PerResourceGroupReader[InternalAPIType]
}

// threadSafeSubscriptionLister is a lister that stores an atomic values in its map.  The key is the index.
// Known indexes today are <subscription> and subscription,resource_group
type perSubscriptionLister[InternalAPIType any] struct {
	// delegate is updated when the contents change
	delegate sync.Map

	hasSynced atomic.Bool
}

func NewPerSubscriptionLister[InternalAPIType any]() PerSubscriptionMaintainer[InternalAPIType] {
	return &perSubscriptionLister[InternalAPIType]{}
}

func (r *perSubscriptionLister[InternalAPIType]) ConfirmSynced() {
	r.hasSynced.Store(true)
}

func (r *perSubscriptionLister[InternalAPIType]) HasSynced() bool {
	return r.hasSynced.Load()
}

func (t *perSubscriptionLister[InternalAPIType]) SetSubscriptionValue(subscriptionName string, reader BasicReader[InternalAPIType]) {
	t.delegate.Store(subscriptionName, reader)
}

func (t *perSubscriptionLister[InternalAPIType]) Subscription(subscriptionName string) BasicReader[InternalAPIType] {
	ret, ok := t.delegate.Load(subscriptionName)
	if !ok {
		return &readOnlyContentLister[InternalAPIType]{
			items:       []*InternalAPIType{},
			itemsByName: make(map[string]*InternalAPIType),
		}
	}
	return ret.(BasicReader[InternalAPIType])
}

type perResourceGroupLister[InternalAPIType any] struct {
	// delegate is updated when the contents change
	delegate sync.Map

	hasSynced atomic.Bool
}

func (r *perResourceGroupLister[InternalAPIType]) ConfirmSynced() {
	r.hasSynced.Store(true)
}

func (r *perResourceGroupLister[InternalAPIType]) HasSynced() bool {
	return r.hasSynced.Load()
}

func (t *perResourceGroupLister[InternalAPIType]) SetResourceGroupValue(subscriptionName, resourceGroupName string, reader BasicReader[InternalAPIType]) {
	t.delegate.Store(subscriptionName+"/"+resourceGroupName, reader)
}

func (t *perResourceGroupLister[InternalAPIType]) ResourceGroup(subscriptionName, resourceGroupName string) BasicReader[InternalAPIType] {
	ret, ok := t.delegate.Load(subscriptionName + "/" + resourceGroupName)
	if !ok {
		return &readOnlyContentLister[InternalAPIType]{
			items:       []*InternalAPIType{},
			itemsByName: make(map[string]*InternalAPIType),
		}
	}
	return ret.(BasicReader[InternalAPIType])
}

var _ PerSubscriptionReader[any] = &perSubscriptionLister[any]{}
var _ PerResourceGroupReader[any] = &perResourceGroupLister[any]{}
