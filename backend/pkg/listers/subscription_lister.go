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
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
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
	items := l.indexer.List()
	result := make([]*arm.Subscription, 0, len(items))
	for _, item := range items {
		sub, ok := item.(*arm.Subscription)
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("expected *arm.Subscription, got %T", item))
		}
		result = append(result, sub)
	}
	return result, nil
}

// Get retrieves a single subscription by subscription ID.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<subscriptionID>
func (l *informerBasedSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	key := strings.ToLower(fmt.Sprintf("/subscriptions/%s", subscriptionID))
	item, exists, err := l.indexer.GetByKey(key)
	if apierrors.IsNotFound(err) {
		return nil, database.NewNotFoundError()
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !exists {
		return nil, database.NewNotFoundError()
	}
	sub, ok := item.(*arm.Subscription)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("expected *arm.Subscription, got %T", item))
	}
	return sub, nil
}
