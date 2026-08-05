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

package corelisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// BillingLister lists and gets billing documents from an informer's indexer.
type BillingLister interface {
	List(ctx context.Context) ([]*billingcosmosstorage.BillingDocument, error)
	GetByID(ctx context.Context, billingDocID string) (*billingcosmosstorage.BillingDocument, error)
	ListForSubscription(ctx context.Context, subscriptionID string) ([]*billingcosmosstorage.BillingDocument, error)
}

// billingDocumentLister implements BillingLister backed by a SharedIndexInformer.
type billingDocumentLister struct {
	indexer cache.Indexer
}

// NewBillingLister creates a BillingLister from a SharedIndexInformer's indexer.
func NewBillingLister(indexer cache.Indexer) BillingLister {
	return &billingDocumentLister{
		indexer: indexer,
	}
}

func (l *billingDocumentLister) List(ctx context.Context) ([]*billingcosmosstorage.BillingDocument, error) {
	return listerutils.ListAll[billingcosmosstorage.BillingDocument](l.indexer)
}

// GetByID retrieves a single billing document by its ID.
// The store key is the billingDocID.
func (l *billingDocumentLister) GetByID(ctx context.Context, billingDocID string) (*billingcosmosstorage.BillingDocument, error) {
	return listerutils.GetByKey[billingcosmosstorage.BillingDocument](l.indexer, billingDocID)
}

// ListForSubscription retrieves all billing documents for a given subscription.
func (l *billingDocumentLister) ListForSubscription(ctx context.Context, subscriptionID string) ([]*billingcosmosstorage.BillingDocument, error) {
	return listerutils.ListFromIndex[billingcosmosstorage.BillingDocument](l.indexer, BySubscription, subscriptionID)
}
