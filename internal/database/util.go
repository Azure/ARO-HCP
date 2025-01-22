package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type queryItemsIterator[T DocumentProperties] struct {
	pager             *runtime.Pager[azcosmos.QueryItemsResponse]
	singlePage        bool
	continuationToken string
	err               error
}

// newqueryItemsIterator is a failable push iterator for a paged query response.
func newQueryItemsIterator[T DocumentProperties](pager *runtime.Pager[azcosmos.QueryItemsResponse]) DBClientIterator[T] {
	return queryItemsIterator[T]{pager: pager}
}

// newQueryItemsSinglePageIterator is a failable push iterator for a paged
// query response that stops at the end of the first page and includes a
// continuation token if additional items are available.
func newQueryItemsSinglePageIterator[T DocumentProperties](pager *runtime.Pager[azcosmos.QueryItemsResponse]) DBClientIterator[T] {
	return queryItemsIterator[T]{pager: pager, singlePage: true}
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter queryItemsIterator[T]) Items(ctx context.Context) DBClientIteratorItem[T] {
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
			for _, item := range response.Items {
				typedDoc, innerDoc, err := typedDocumentUnmarshal[T](item)
				if err != nil {
					iter.err = err
					return
				}

				if !yield(typedDoc.ID, innerDoc) {
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
func (iter queryItemsIterator[T]) GetContinuationToken() string {
	return iter.continuationToken
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter queryItemsIterator[T]) GetError() error {
	return iter.err
}
