// Copyright 2025 Microsoft Corporation
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

package billingcosmosstorage

import (
	"context"
	"encoding/json"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type queryBillingIterator struct {
	pager             *runtime.Pager[azcosmos.QueryItemsResponse]
	singlePage        bool
	continuationToken string
	err               error
}

// newQueryBillingIterator is a failable push iterator for billing document queries.
func newQueryBillingIterator(pager *runtime.Pager[azcosmos.QueryItemsResponse]) cosmosstorageutils.DBClientIterator[BillingDocument] {
	return &queryBillingIterator{pager: pager}
}

// newQueryBillingSinglePageIterator is a failable push iterator for billing documents
// that stops at the end of the first page and includes a continuation token if
// additional items are available.
func newQueryBillingSinglePageIterator(pager *runtime.Pager[azcosmos.QueryItemsResponse]) cosmosstorageutils.DBClientIterator[BillingDocument] {
	return &queryBillingIterator{pager: pager, singlePage: true}
}

// Items returns a push iterator that can be used directly in for/range loops.
func (iter *queryBillingIterator) Items(ctx context.Context) cosmosstorageutils.DBClientIteratorItem[BillingDocument] {
	return func(yield func(string, *BillingDocument) bool) {
		for iter.pager.More() {
			response, err := iter.pager.NextPage(ctx)
			if err != nil {
				iter.err = err
				return
			}
			if iter.singlePage && response.ContinuationToken != nil {
				iter.continuationToken = *response.ContinuationToken
			}
			for _, itemJSON := range response.Items {
				var doc BillingDocument
				if err := json.Unmarshal(itemJSON, &doc); err != nil {
					iter.err = err
					return
				}

				if !yield(doc.ID, &doc) {
					return
				}
			}
			if iter.singlePage {
				return
			}
		}
	}
}

// GetContinuationToken returns a continuation token for pagination.
func (iter *queryBillingIterator) GetContinuationToken() string {
	return iter.continuationToken
}

// GetError returns any error that occurred during iteration.
func (iter *queryBillingIterator) GetError() error {
	return iter.err
}
