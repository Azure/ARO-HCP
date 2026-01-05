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

func (r *perSubscriptionLister[InternalAPIType]) ConfirmSynced() bool {
	return r.hasSynced.Swap(true)
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
		return &readOnlyContentLister[InternalAPIType]{}
	}
	return ret.(BasicReader[InternalAPIType])
}

type perResourceGroupLister[InternalAPIType any] struct {
	// delegate is updated when the contents change
	delegate sync.Map

	hasSynced atomic.Bool
}

func (r *perResourceGroupLister[InternalAPIType]) ConfirmSynced() bool {
	return r.hasSynced.Swap(true)
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
		return &readOnlyContentLister[InternalAPIType]{}
	}
	return ret.(BasicReader[InternalAPIType])
}

var _ PerSubscriptionReader[any] = &perSubscriptionLister[any]{}
var _ PerResourceGroupReader[any] = &perResourceGroupLister[any]{}
