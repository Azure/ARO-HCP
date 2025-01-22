package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// XXX The Azure SDK for Go does not support cross-partition Cosmos DB
//     queries which means the partition keys of a Cosmos DB container
//     cannot be discovered through the Go SDK.
//
//     The Azure SDK for other languages like .NET, Java, and Python
//     already support cross-partition queries, so there is reason to
//     hope the Go SDK may some day catch up.
//
//     In the meantime, this container serves as a workaround. This is
//     a single-partition container that simply collects the partition
//     keys of the Resources container, like an index.
//
//     Once [1] is fixed we could simply drop this container and begin
//     using cross-partition queries on the Resources container.
//
//     [1] https://github.com/Azure/azure-sdk-for-go/issues/18578

const (
	// partitionKeysPartitionKey is a constant (and somewhat pointed)
	// partition key value for all items in the PartitionKeys container.
	partitionKeysPartitionKey = "azure-sdk-for-go-issue-18578"
)

// partitionKeyDocument holds a partition key for another container as
// the document ID. The PartitionKey for this document is held constant.
type partitionKeyDocument struct {
	baseDocument
	PartitionKey string `json:"partitionKey,omitempty"`
}

// partitionKeyIterator implements DBClientIterator for subscriptions.
type partitionKeyIterator struct {
	client DBClient
	pager  *runtime.Pager[azcosmos.QueryItemsResponse]
	err    error
}

func upsertPartitionKey(ctx context.Context, containerClient *azcosmos.ContainerClient, id string) error {
	pk := azcosmos.NewPartitionKeyString(partitionKeysPartitionKey)

	data, err := json.Marshal(&partitionKeyDocument{
		baseDocument: baseDocument{ID: id},
		PartitionKey: partitionKeysPartitionKey,
	})
	if err != nil {
		return err
	}

	_, err = containerClient.UpsertItem(ctx, pk, data, nil)

	return err
}

func listPartitionKeys(containerClient *azcosmos.ContainerClient, client DBClient) DBClientIterator[SubscriptionDocument] {
	pk := azcosmos.NewPartitionKeyString(partitionKeysPartitionKey)

	pager := containerClient.NewQueryItemsPager("SELECT c.id FROM c", pk, nil)

	return &partitionKeyIterator{client: client, pager: pager}
}

func (iter *partitionKeyIterator) Items(ctx context.Context) DBClientIteratorItem[SubscriptionDocument] {
	return func(yield func(*SubscriptionDocument) bool) {
		for iter.pager.More() {
			response, err := iter.pager.NextPage(ctx)
			if err != nil {
				iter.err = err
				return
			}
			for _, item := range response.Items {
				var doc baseDocument

				// Since the query just selects the "id" field,
				// baseDocument is sufficient for unmarshalling.
				err = json.Unmarshal(item, &doc)
				if err != nil {
					iter.err = err
					return
				}

				subscription, err := iter.client.GetSubscriptionDoc(ctx, doc.ID)
				if err != nil {
					iter.err = err
					return
				}

				if !yield(subscription) {
					return
				}
			}
		}
	}
}

func (iter *partitionKeyIterator) GetContinuationToken() string {
	return ""
}

func (iter *partitionKeyIterator) GetError() error {
	return iter.err
}
