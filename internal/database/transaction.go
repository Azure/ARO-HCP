package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type DBTransactionCallback func(DBTransactionResult)

type DBTransaction interface {
	// GetPartitionKey returns the transaction's partition key.
	GetPartitionKey() azcosmos.PartitionKey

	// ReadDoc adds a read request to the transaction whose result
	// is obtained through the DBTransactionResult interface.
	ReadDoc(itemID string, o *azcosmos.TransactionalBatchItemOptions)

	// DeleteDoc adds a delete request to the transaction.
	DeleteDoc(itemID string, o *azcosmos.TransactionalBatchItemOptions)

	// CreateResourceDoc adds a create request to the transaction
	// and returns the tentative item ID.
	CreateResourceDoc(doc *ResourceDocument, o *azcosmos.TransactionalBatchItemOptions) string

	// PatchResourceDoc adds a set of patch operations to the transaction.
	PatchResourceDoc(itemID string, ops ResourceDocumentPatchOperations, o *azcosmos.TransactionalBatchItemOptions)

	// CreateOperationDoc adds a create request to the transaction
	// and returns the tentative item ID.
	CreateOperationDoc(doc *OperationDocument, o *azcosmos.TransactionalBatchItemOptions) string

	// PatchOperationDoc adds a set of patch operations to the transaction.
	PatchOperationDoc(itemID string, ops OperationDocumentPatchOperations, o *azcosmos.TransactionalBatchItemOptions)

	// OnSuccess adds a function to call if the transaction executes successfully.
	OnSuccess(callback DBTransactionCallback)

	// Execute submits the prepared transaction.
	Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error)
}

type DBTransactionResult interface {
	// GetResourceDoc returns the ResourceDocument for itemID.
	// The ResourceDocument is only available if the transaction was
	// executed with the EnableContentResponseOnWrite option set, or
	// the document was requested with DBTransaction.ReadDoc.
	GetResourceDoc(itemID string) (*ResourceDocument, error)

	// GetOperationDoc returns the OperationDocument for itemID.
	// The OperationDocument is only available if the transaction was
	// executed with the EnableContentResponseOnWrite option set, or
	// the document was requested with DBTransaction.ReadDoc.
	GetOperationDoc(itemID string) (*OperationDocument, error)
}

// ErrItemNotFound occurs when the requested item ID was not found,
// such as in a DBTransactionResult.
var ErrItemNotFound = errors.New("item not found")

// ErrWrongPartition occurs in a DBTransaction create step when the
// document has a partition key that differs from the transaction's
// partition key.
var ErrWrongPartition = errors.New("wrong partition key for transaction")

var _ DBTransaction = &cosmosDBTransaction{}

type cosmosDBTransactionStep func(b *azcosmos.TransactionalBatch) (string, error)

type cosmosDBTransaction struct {
	pk        azcosmos.PartitionKey
	client    *azcosmos.ContainerClient
	steps     []cosmosDBTransactionStep
	onSuccess []DBTransactionCallback
}

func newCosmosDBTransaction(pk azcosmos.PartitionKey, client *azcosmos.ContainerClient) *cosmosDBTransaction {
	return &cosmosDBTransaction{pk, client, nil, nil}
}

func (t *cosmosDBTransaction) GetPartitionKey() azcosmos.PartitionKey {
	return t.pk
}

func (t *cosmosDBTransaction) ReadDoc(itemID string, o *azcosmos.TransactionalBatchItemOptions) {
	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		b.ReadItem(itemID, o)
		return itemID, nil
	})
}

func (t *cosmosDBTransaction) DeleteDoc(itemID string, o *azcosmos.TransactionalBatchItemOptions) {
	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		b.DeleteItem(itemID, o)
		return itemID, nil
	})
}

func (t *cosmosDBTransaction) CreateResourceDoc(doc *ResourceDocument, o *azcosmos.TransactionalBatchItemOptions) string {
	typedDoc := newTypedDocument(doc.ResourceID.SubscriptionID, doc.ResourceID.ResourceType)

	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		var data []byte
		var err error

		if reflect.DeepEqual(t.pk, typedDoc.getPartitionKey()) {
			data, err = typedDocumentMarshal(typedDoc, doc)
		} else {
			err = ErrWrongPartition
		}

		if err != nil {
			return "", fmt.Errorf("failed to marshal Cosmos DB item for '%s': %w", doc.ResourceID, err)
		}

		b.CreateItem(data, o)
		return typedDoc.ID, nil
	})

	return typedDoc.ID
}

func (t *cosmosDBTransaction) PatchResourceDoc(itemID string, ops ResourceDocumentPatchOperations, o *azcosmos.TransactionalBatchItemOptions) {
	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		b.PatchItem(itemID, ops.PatchOperations, o)
		return itemID, nil
	})
}

func (t *cosmosDBTransaction) CreateOperationDoc(doc *OperationDocument, o *azcosmos.TransactionalBatchItemOptions) string {
	typedDoc := newTypedDocument(doc.ExternalID.SubscriptionID, OperationResourceType)
	typedDoc.TimeToLive = operationTimeToLive

	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		var data []byte
		var err error

		if reflect.DeepEqual(t.pk, typedDoc.getPartitionKey()) {
			data, err = typedDocumentMarshal(typedDoc, doc)
		} else {
			err = ErrWrongPartition
		}

		if err != nil {
			return "", fmt.Errorf("failed to marshal Cosmos DB item for operation '%s': %w", typedDoc.ID, err)
		}

		b.CreateItem(data, o)
		return typedDoc.ID, nil
	})

	return typedDoc.ID
}

func (t *cosmosDBTransaction) PatchOperationDoc(itemID string, ops OperationDocumentPatchOperations, o *azcosmos.TransactionalBatchItemOptions) {
	t.steps = append(t.steps, func(b *azcosmos.TransactionalBatch) (string, error) {
		b.PatchItem(itemID, ops.PatchOperations, o)
		return itemID, nil
	})
}

func (t *cosmosDBTransaction) OnSuccess(callback DBTransactionCallback) {
	if callback != nil {
		t.onSuccess = append(t.onSuccess, callback)
	}
}

func (t *cosmosDBTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
	result := newCosmosDBTransactionResult()

	if len(t.steps) > 0 {
		batch := t.client.NewTransactionalBatch(t.pk)

		// Execute the queued steps to prepare the transaction. Collect
		// the item ID of each step to pair with the operation results.
		itemIDs := make([]string, 0, len(t.steps))
		for _, step := range t.steps {
			id, err := step(&batch)
			if err != nil {
				return nil, err
			}
			itemIDs = append(itemIDs, id)
		}

		response, err := t.client.ExecuteTransactionalBatch(ctx, batch, o)
		if err != nil {
			return nil, err
		}

		if !response.Success {
			for _, result := range response.OperationResults {
				if result.StatusCode != http.StatusFailedDependency {
					// FIXME Return an error type that allows checking the StatusCode.
					//       I was tempted to use azcore.ResponseError but it formats
					//       poorly in a log message without an http.Response.
					return nil, fmt.Errorf("%d %s", result.StatusCode, http.StatusText(int(result.StatusCode)))
				}
			}
		}

		// The two slices SHOULD be of equal length.
		safeStop := min(len(itemIDs), len(response.OperationResults))
		for i := 0; i < safeStop; i++ {
			if len(response.OperationResults[i].ResourceBody) > 0 {
				result.items[itemIDs[i]] = response.OperationResults[i].ResourceBody
			}
		}
	}

	for _, callback := range t.onSuccess {
		callback(result)
	}

	return result, nil
}

var _ DBTransactionResult = &cosmosDBTransactionResult{}

type cosmosDBTransactionResult struct {
	items map[string]json.RawMessage
}

func newCosmosDBTransactionResult() *cosmosDBTransactionResult {
	return &cosmosDBTransactionResult{make(map[string]json.RawMessage)}
}

func getCosmosDBTransactionResultDoc[T DocumentProperties](r *cosmosDBTransactionResult, itemID string) (*T, error) {
	data, ok := r.items[itemID]
	if !ok {
		return nil, ErrItemNotFound
	}

	typedDoc, innerDoc, err := typedDocumentUnmarshal[T](data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item '%s': %w", itemID, err)
	}

	// Verify the document ID agrees with the requested ID.
	if typedDoc.ID != itemID {
		return nil, ErrItemNotFound
	}

	return innerDoc, nil
}

func (r *cosmosDBTransactionResult) GetResourceDoc(itemID string) (*ResourceDocument, error) {
	return getCosmosDBTransactionResultDoc[ResourceDocument](r, itemID)
}

func (r *cosmosDBTransactionResult) GetOperationDoc(itemID string) (*OperationDocument, error) {
	return getCosmosDBTransactionResultDoc[OperationDocument](r, itemID)
}
