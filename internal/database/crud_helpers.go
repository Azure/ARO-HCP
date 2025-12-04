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
		err := &azcore.ResponseError{StatusCode: http.StatusNotFound}
		return nil, fmt.Errorf("failed to read Resources container item for '%s': %w", completeResourceID, err)
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", completeResourceID, err)
	}
	cosmosObj := &obj

	// Replace the key field from Cosmos with the given resourceID,
	// which typically comes from the URL. This helps preserve the
	// casing of the resource group and resource name from the URL
	// to meet RPC requirements:
	//
	// Put Resource | Arguments
	//
	// The resource group names and resource names should be matched
	// case insensitively. ... Additionally, the Resource Provier must
	// preserve the casing provided by the user. The service must return
	// the most recently specified casing to the client and must not
	// normalize or return a toupper or tolower form of the resource
	// group or resource name. The resource group name and resource
	// name must come from the URL and not the request body.
	retAsResourceProperties, ok := any(cosmosObj).(ResourceProperties)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement ResourceProperties interface", cosmosObj)
	}
	retAsResourceProperties.SetResourceID(completeResourceID)

	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func list[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, resourceType azcorearm.ResourceType, prefix *azcorearm.ResourceID, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
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

	query += " AND STRINGEQUALS(c.resourceType, @resourceType, true)"
	queryParameter := azcosmos.QueryParameter{
		Name:  "@resourceType",
		Value: resourceType.String(),
	}
	queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)

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

	if ptr.Deref(options.PageSizeHint, -1) > 0 {
		return newQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	} else {
		return newQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
}

func addCreateToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return "", fmt.Errorf("type %T does not implement ResourceProperties interface", newObj)
	}
	cosmosData := cosmosPersistable.GetCosmosData()

	newCosmosUID := uuid.New()
	cosmosPersistable.SetCosmosDocumentData(newCosmosUID)

	cosmosObj, err := InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", fmt.Errorf("failed to convert internal object to Cosmos object: %w", err)
	}

	transaction.AddStep(
		func(b *azcosmos.TransactionalBatch) (string, error) {
			data, err := json.Marshal(cosmosObj)

			if err != nil {
				return "", fmt.Errorf("failed to marshal Cosmos DB item for '%s': %w", cosmosData.ID, err)
			}

			b.CreateItem(data, opts)
			return newCosmosUID.String(), nil
		},
	)

	return newCosmosUID.String(), nil
}
