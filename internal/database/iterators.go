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

package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type queryResourcesIterator[T any] struct {
	pager             *runtime.Pager[azcosmos.QueryItemsResponse]
	singlePage        bool
	continuationToken string
	err               error
}

// newQueryResourcesIterator is a failable push iterator for a paged query response.
func newQueryResourcesIterator[T any](pager *runtime.Pager[azcosmos.QueryItemsResponse]) DBClientIterator[T] {
	return &queryResourcesIterator[T]{pager: pager}
}

// newQueryItemsSinglePageIterator is a failable push iterator for a paged
// query response that stops at the end of the first page and includes a
// continuation token if additional items are available.
func newQueryResourcesSinglePageIterator[T any](pager *runtime.Pager[azcosmos.QueryItemsResponse]) DBClientIterator[T] {
	return &queryResourcesIterator[T]{pager: pager, singlePage: true}
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *queryResourcesIterator[T]) Items(ctx context.Context) DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
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
				var obj T
				if err := json.Unmarshal(itemJSON, &obj); err != nil {
					iter.err = err
					return
				}
				decodedItem := &obj

				decodedItemAsResourceProperties, ok := any(decodedItem).(ResourceProperties)
				if !ok {
					iter.err = fmt.Errorf("type %T does not implement DocumentProperties interface", decodedItem)
					return
				}

				if !yield(decodedItemAsResourceProperties.GetTypedDocument().ID, decodedItem) {
					return
				}
			}
			if iter.singlePage {
				return
			}
		}
	}
}

// GetContinuationToken returns a continuation token that can be used to obtain
// the next page of results. This is only set when the iterator was created with
// NewQueryItemsSinglePageIterator and additional items are available.
func (iter queryResourcesIterator[T]) GetContinuationToken() string {
	return iter.continuationToken
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter queryResourcesIterator[T]) GetError() error {
	return iter.err
}
