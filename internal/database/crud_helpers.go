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
	"net/http"

	"github.com/google/uuid"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
)

func get[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, completeResourceID *azcorearm.ResourceID) (*InternalAPIType, error) {
	var responseItem []byte

	pk := NewPartitionKey(completeResourceID.SubscriptionID)

	const query = "SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, @resourceType, true) AND STRINGEQUALS(c.properties.resourceId, @resourceId, true)"
	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceType",
				Value: completeResourceID.ResourceType.String(),
			},
			{
				Name:  "@resourceId",
				Value: completeResourceID.String(),
			},
		},
	}

	queryPager := containerClient.NewQueryItemsPager(query, pk, &opt)
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to advance page while querying Resources container for '%s': %w", completeResourceID, err)
		}

		for _, item := range queryResponse.Items {
			// Let the pager finish to ensure we get a single result.
			if responseItem == nil {
				responseItem = item
			} else {
				return nil, ErrAmbiguousResult
			}
		}
	}

	if responseItem == nil {
		// Fabricate a "404 Not Found" ResponseError to wrap.
		err := &azcore.ResponseError{
			ErrorCode:  http.StatusText(http.StatusNotFound),
			StatusCode: http.StatusNotFound,
		}
		return nil, fmt.Errorf("failed to read Resources container item for '%s': %w", completeResourceID, err)
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", completeResourceID, err)
	}
	cosmosObj := &obj

	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func list[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, resourceType *azcorearm.ResourceType, prefix *azcorearm.ResourceID, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	pk := NewPartitionKey(prefix.SubscriptionID)

	query := "SELECT * FROM c WHERE STARTSWITH(c.properties.resourceId, @prefix, true)"

	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@prefix",
				Value: prefix.String() + "/",
			},
		},
	}

	if resourceType != nil {
		query += " AND STRINGEQUALS(c.resourceType, @resourceType, true)"
		queryParameter := azcosmos.QueryParameter{
			Name:  "@resourceType",
			Value: resourceType.String(),
		}
		queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
	}

	if options != nil {
		// XXX The Cosmos DB REST API gives special meaning to -1 for "x-ms-max-item-count"
		//     but it's not clear if it treats all negative values equivalently. The Go SDK
		//     passes the PageSizeHint value as provided so normalize negative values to -1
		//     to be safe.
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	pager := containerClient.NewQueryItemsPager(query, pk, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return newQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	} else {
		return newQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
}

// serializeItem will create a CosmosUID if it doesn't exist, otherwise uses what exists.  This makes it compatible with
// create, replace, and create
func serializeItem[InternalAPIType, CosmosAPIType any](newObj *InternalAPIType) (string, *azcosmos.PartitionKey, []byte, error) {
	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return "", nil, nil, fmt.Errorf("type %T does not implement ResourceProperties interface", newObj)
	}
	cosmosData := cosmosPersistable.GetCosmosData()

	var cosmosUID string
	if len(cosmosData.CosmosUID) != 0 {
		cosmosUID = cosmosData.CosmosUID
	} else {
		cosmosUID = uuid.New().String()
		cosmosPersistable.SetCosmosDocumentData(cosmosUID)
	}

	cosmosObj, err := InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to convert internal object to Cosmos object: %w", err)
	}
	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to marshal Cosmos DB item for '%s': %w", cosmosData.ItemID, err)
	}

	return cosmosUID, &cosmosData.PartitionKey, data, nil
}

func addCreateToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	newCosmosUID, _, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}

	transaction.AddStep(
		func(b *azcosmos.TransactionalBatch) (string, error) {
			b.CreateItem(data, opts)
			return newCosmosUID, nil
		},
	)

	return newCosmosUID, nil
}

func addReplaceToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosUID, _, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}

	transaction.AddStep(
		func(b *azcosmos.TransactionalBatch) (string, error) {
			// TODO decide if, when, and how we ever add etags.  Currently we do unconditional replaces.
			b.ReplaceItem(cosmosUID, data, opts)
			return cosmosUID, nil
		},
	)

	return cosmosUID, nil
}

func create[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	newCosmosUID, partitionKey, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	opts.EnableContentResponseOnWrite = true
	responseItem, err := containerClient.CreateItem(ctx, *partitionKey, data, opts)
	if err != nil {
		return nil, err
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem.Value, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item for '%s': %w", newCosmosUID, err)
	}
	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](&obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func replace[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	newCosmosUID, partitionKey, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	opts.EnableContentResponseOnWrite = true
	responseItem, err := containerClient.ReplaceItem(ctx, *partitionKey, newCosmosUID, data, opts)
	if err != nil {
		return nil, err
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem.Value, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item for '%s': %w", newCosmosUID, err)
	}
	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](&obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}
