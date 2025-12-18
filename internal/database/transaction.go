package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
)

type DBTransactionCallback func(DBTransactionResult)

type DBTransaction interface {
	// AddStep adds a transaction function to the list to perform
	AddStep(CosmosDBTransactionStep)

	// GetPartitionKey returns the transaction's partition key.
	GetPartitionKey() string

	// OnSuccess adds a function to call if the transaction executes successfully.
	OnSuccess(callback DBTransactionCallback)

	// Execute submits the prepared transaction.
	Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error)
}

type DBTransactionResult interface {
	// GetItem returns the internal API representation for the cosmosUID.
	// That is consistent with other returns from our database layer.
	// The Item is only available if the transaction was
	// executed with the EnableContentResponseOnWrite option set, or
	// the document was requested with DBTransaction.ReadDoc.
	GetItem(cosmosUID string) (any, error)
}

// ErrItemNotFound occurs when the requested item ID was not found,
// such as in a DBTransactionResult.
var ErrItemNotFound = errors.New("item not found")

// ErrWrongPartition occurs in a DBTransaction create step when the
// document has a partition key that differs from the transaction's
// partition key.
var ErrWrongPartition = errors.New("wrong partition key for transaction")

var _ DBTransaction = &cosmosDBTransaction{}

type CosmosDBTransactionStep func(b *azcosmos.TransactionalBatch) (string, error)

type cosmosDBTransaction struct {
	pk        string
	client    *azcosmos.ContainerClient
	steps     []CosmosDBTransactionStep
	onSuccess []DBTransactionCallback
}

func newCosmosDBTransaction(pk string, client *azcosmos.ContainerClient) *cosmosDBTransaction {
	return &cosmosDBTransaction{
		pk:        strings.ToLower(pk),
		client:    client,
		steps:     nil,
		onSuccess: nil}
}

func (t *cosmosDBTransaction) GetPartitionKey() string {
	return t.pk
}

func (t *cosmosDBTransaction) AddStep(stepFn CosmosDBTransactionStep) {
	t.steps = append(t.steps, stepFn)
}

func (t *cosmosDBTransaction) OnSuccess(callback DBTransactionCallback) {
	if callback != nil {
		t.onSuccess = append(t.onSuccess, callback)
	}
}

func (t *cosmosDBTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
	result := newCosmosDBTransactionResult()

	if len(t.steps) > 0 {
		batch := t.client.NewTransactionalBatch(azcosmos.NewPartitionKeyString(t.pk))

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
			for step, result := range response.OperationResults {
				if result.StatusCode != http.StatusFailedDependency {
					// FIXME Return an error type that allows checking the StatusCode.
					//       I was tempted to use azcore.ResponseError but it formats
					//       poorly in a log message without an http.Response.
					return nil, fmt.Errorf("transaction step %d of %d failed with %d %s", step+1, len(response.OperationResults), result.StatusCode, http.StatusText(int(result.StatusCode)))
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

func getCastResult[InternalAPIType, CosmosAPIType any](r *cosmosDBTransactionResult, cosmosUID string) (*InternalAPIType, error) {
	data, ok := r.items[cosmosUID]
	if !ok {
		return nil, ErrItemNotFound
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(data, &cosmosObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item '%s': %w", cosmosUID, err)
	}

	return CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
}

func (r *cosmosDBTransactionResult) GetItem(cosmosUID string) (any, error) {
	data, ok := r.items[cosmosUID]
	if !ok {
		return nil, ErrItemNotFound
	}

	var typedDoc TypedDocument
	err := json.Unmarshal(data, &typedDoc)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(typedDoc.ResourceType) {
	case strings.ToLower(api.ClusterResourceType.String()):
		return getCastResult[api.HCPOpenShiftCluster, HCPCluster](r, cosmosUID)
	case strings.ToLower(api.NodePoolResourceType.String()):
		return getCastResult[api.HCPOpenShiftClusterNodePool, NodePool](r, cosmosUID)
	case strings.ToLower(api.ExternalAuthResourceType.String()):
		return getCastResult[api.HCPOpenShiftClusterExternalAuth, ExternalAuth](r, cosmosUID)
	default:
		return nil, fmt.Errorf("unknown resource type '%s'", typedDoc.ResourceType)
	}
}
