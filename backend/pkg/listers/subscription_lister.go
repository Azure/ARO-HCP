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

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// InformerBasedSubscriptionLister lists and gets subscriptions from an informer's indexer.
type SubscriptionLister interface {
	List(ctx context.Context) ([]*arm.Subscription, error)
	Get(ctx context.Context, subscriptionID string) (*arm.Subscription, error)
}

// informerBasedSubscriptionLister implements SubscriptionLister backed by a SharedIndexInformer.
type informerBasedSubscriptionLister struct {
	indexer cache.Indexer
}

// NewSubscriptionLister creates an SubscriptionLister from a SharedIndexInformer's indexer.
func NewSubscriptionLister(indexer cache.Indexer) SubscriptionLister {
	return &informerBasedSubscriptionLister{
		indexer: indexer,
	}
}

func (l *informerBasedSubscriptionLister) List(ctx context.Context) ([]*arm.Subscription, error) {
	return listAll[arm.Subscription](l.indexer)
}

// Get retrieves a single subscription by subscription ID.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<subscriptionID>
func (l *informerBasedSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	key := arm.ToSubscriptionResourceIDString(subscriptionID)
	return getByKey[arm.Subscription](l.indexer, key)
}
