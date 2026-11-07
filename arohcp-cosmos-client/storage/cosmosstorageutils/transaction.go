// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

package cosmosstorageutils

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type DBTransactionCallback func(DBTransactionResult)

type DBTransaction interface {
	// AddStep adds a transaction function to the list to perform
	AddStep(CosmosDBTransactionStepDetails, CosmosDBTransactionStep)

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

type CosmosDBTransactionStep func(b *azcosmos.TransactionalBatch) (string, error)

type CosmosDBTransactionDetails struct {
	PartitionKey string                           `json:"partitionKey"`
	Steps        []CosmosDBTransactionStepDetails `json:"steps"`
}

type CosmosDBTransactionStepDetails struct {
	ActionType string      `json:"actionType"`
	CosmosID   string      `json:"cosmosID"`
	ResourceID string      `json:"resourceID"`
	GoType     string      `json:"goType"`
	Etag       azcore.ETag `json:"etag"`
}
