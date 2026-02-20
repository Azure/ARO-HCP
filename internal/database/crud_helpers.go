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
	"errors"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func OldResourceIDToCosmosID(resourceID *azcorearm.ResourceID) (string, error) {
	if resourceID == nil {
		return "", errors.New("resource ID is nil")
	}
	return oldResourceIDStringToCosmosID(resourceID.String())
}

func oldResourceIDStringToCosmosID(resourceID string) (string, error) {
	if len(resourceID) == 0 {
		return "", errors.New("resource ID is empty")
	}
	// cosmos uses a REST API, which means that IDs that contain slashes cause problems with URL handling.
	// We chose | because that is a delimiter that is not allowed inside of an ARM resource ID because it is a separator
	// for multiple resource IDs.
	return strings.ReplaceAll(strings.ToLower(resourceID), "/", "|"), nil
}

// TODO this will eventually be the standard GET, but until we rewrite all records with new `id` values, it must remain separate and specifically called.
func getByItemID[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, cosmosID string) (*InternalAPIType, error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}

	responseItem, err := containerClient.ReadItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosID, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem.Value, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", cosmosID, err)
	}
	cosmosObj := &obj

	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func get[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, completeResourceID *azcorearm.ResourceID) (*InternalAPIType, error) {
	// try the ID format first because it'll be more often correct.
	oldExactCosmosID, err := OldResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	ret, err := getByItemID[InternalAPIType, CosmosAPIType](ctx, containerClient, partitionKeyString, oldExactCosmosID)
	if err == nil {
		return ret, nil
	}
	if !IsResponseError(err, http.StatusNotFound) {
		return nil, utils.TrackError(err)
	}

	// now try the new format in case we've started migrating or rolled back.
	newExactCosmosID, err := arm.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return getByItemID[InternalAPIType, CosmosAPIType](ctx, containerClient, partitionKeyString, newExactCosmosID)
}

func list[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, resourceType *azcorearm.ResourceType, prefix *azcorearm.ResourceID, options *DBClientListResourceDocsOptions, untypedNonRecursive bool) (DBClientIterator[InternalAPIType], error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	if prefix == nil && resourceType == nil {
		return nil, fmt.Errorf("prefix or resource type is required")
	}

	query := ""
	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
	}
	if prefix == nil {
		query = "SELECT * FROM c"
	} else {
		query = "SELECT * FROM c WHERE STARTSWITH(c.resourceID, @prefix, true)"
		queryOptions = azcosmos.QueryOptions{
			PageSizeHint: -1,
			QueryParameters: []azcosmos.QueryParameter{
				{
					Name:  "@prefix",
					Value: prefix.String() + "/",
				},
			},
		}
	}

	if resourceType != nil {
		if prefix == nil {
			query += " WHERE STRINGEQUALS(c.resourceType, @resourceType, true)"
		} else {
			query += " AND STRINGEQUALS(c.resourceType, @resourceType, true)"
		}
		queryParameter := azcosmos.QueryParameter{
			Name:  "@resourceType",
			Value: resourceType.String(),
		}
		queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
	}

	if untypedNonRecursive {
		// resourceIDs are /subscriptions/<name>/resourceGroups/<name>/providers/RH/type[0]/<name>/type[1]/<name.../type[n]/<name>
		// if we count the slashes, then a non-recursive list should only include resource ID that have numSlashesInPrefix+2 for most
		requiredNumSlashes := strings.Count(prefix.String(), "/") + 2
		if strings.EqualFold(prefix.ResourceType.Type, "resourceGroups") {
			// if it's a resourceGroup, then we need to add four to select clusters
			requiredNumSlashes = strings.Count(prefix.String(), "/") + 4
		}

		// no sql injection risk because it's an int we control
		query += fmt.Sprintf(" AND (LENGTH(c.resourceID) - LENGTH(REPLACE(c.resourceID, '/', ''))) = %d", requiredNumSlashes)
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
	var partitionKey azcosmos.PartitionKey
	if len(partitionKeyString) > 0 {
		partitionKey = azcosmos.NewPartitionKeyString(partitionKeyString)
	} else {
		partitionKey = azcosmos.NewPartitionKey()
	}

	pager := containerClient.NewQueryItemsPager(query, partitionKey, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return newQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	} else {
		return newQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
}

// serializeItem will create a CosmosUID if it doesn't exist, otherwise uses what exists.  This makes it compatible with
// create, replace, and create
func serializeItem[InternalAPIType, CosmosAPIType any](newObj *InternalAPIType) (*arm.CosmosMetadata, []byte, error) {
	cosmosPersistable, ok := any(newObj).(arm.CosmosPersistable)
	if !ok {
		return nil, nil, fmt.Errorf("type %T does not implement CosmosPersistable interface", newObj)
	}
	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosUID := cosmosData.GetCosmosUID()
	if len(cosmosUID) == 0 {
		return nil, nil, fmt.Errorf("no cosmos id found in object")
	}
	if !strings.EqualFold(cosmosUID, strings.ToLower(cosmosUID)) {
		return nil, nil, fmt.Errorf("invalid cosmos id found in object")
	}
	if !strings.EqualFold(cosmosData.GetPartitionKey(), strings.ToLower(cosmosData.GetPartitionKey())) {
		return nil, nil, fmt.Errorf("invalid partitionKey found in object")
	}

	cosmosObj, err := InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert internal object to Cosmos object: %w", err)
	}
	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Cosmos DB item for '%s': %w", cosmosData.ResourceID, err)
	}

	return cosmosData, data, nil
}

func addCreateToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	partitionKeyString := transaction.GetPartitionKey()
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return "", fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}
	if partitionKeyString != cosmosMetadata.GetPartitionKey() {
		return "", fmt.Errorf("item partition key does not match partition key: %q vs %q", partitionKeyString, cosmosMetadata.GetPartitionKey())
	}
	transactionDetails := CosmosDBTransactionStepDetails{
		ActionType: "Create",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosMetadata.GetCosmosUID(),
		ResourceID: cosmosMetadata.ResourceID.String(),
	}

	transaction.AddStep(
		transactionDetails,
		func(b *azcosmos.TransactionalBatch) (string, error) {
			b.CreateItem(data, opts)
			return cosmosMetadata.GetCosmosUID(), nil
		},
	)

	return cosmosMetadata.GetCosmosUID(), nil
}

func addReplaceToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	partitionKeyString := transaction.GetPartitionKey()
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return "", fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}
	if partitionKeyString != cosmosMetadata.GetPartitionKey() {
		return "", fmt.Errorf("item partition key does not match partition key: %q vs %q", partitionKeyString, cosmosMetadata.GetPartitionKey())
	}
	transactionDetails := CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosMetadata.GetCosmosUID(),
		ResourceID: cosmosMetadata.ResourceID.String(),
	}

	if opts == nil {
		opts = &azcosmos.TransactionalBatchItemOptions{}
	}
	if len(cosmosMetadata.CosmosETag) > 0 {
		opts.IfMatchETag = &cosmosMetadata.CosmosETag
	}

	transaction.AddStep(
		transactionDetails,
		func(b *azcosmos.TransactionalBatch) (string, error) {
			// TODO decide if, when, and how we ever add etags.  Currently we do unconditional replaces.
			b.ReplaceItem(cosmosMetadata.GetCosmosUID(), data, opts)
			return cosmosMetadata.GetCosmosUID(), nil
		},
	)

	return cosmosMetadata.GetCosmosUID(), nil
}

func create[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	if partitionKeyString != cosmosMetadata.GetPartitionKey() {
		return nil, fmt.Errorf("item partition key does not match partition key: %q vs %q", partitionKeyString, cosmosMetadata.GetPartitionKey())
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	opts.EnableContentResponseOnWrite = true

	responseItem, err := containerClient.CreateItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), data, opts)
	if err != nil {
		return nil, err
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem.Value, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item for '%s': %w", cosmosMetadata.ResourceID, err)
	}
	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](&obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func replace[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	if partitionKeyString != cosmosMetadata.GetPartitionKey() {
		return nil, fmt.Errorf("item partition key does not match partition key: %q vs %q", partitionKeyString, cosmosMetadata.GetPartitionKey())
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	if len(cosmosMetadata.CosmosETag) > 0 {
		opts.IfMatchEtag = &cosmosMetadata.CosmosETag
	}
	opts.EnableContentResponseOnWrite = true

	responseItem, err := containerClient.ReplaceItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosMetadata.GetCosmosUID(), data, opts)
	if err != nil {
		return nil, err
	}

	var obj CosmosAPIType
	if err := json.Unmarshal(responseItem.Value, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Cosmos DB item for '%s': %w", cosmosMetadata.ResourceID, err)
	}
	internalObj, err := CosmosToInternal[InternalAPIType, CosmosAPIType](&obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Cosmos object to internal type: %w", err)
	}

	return internalObj, nil
}

func deleteResource(ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, resourceID *azcorearm.ResourceID) error {
	typedObj, err := get[TypedDocument, TypedDocument](ctx, containerClient, partitionKeyString, resourceID)
	if IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = containerClient.DeleteItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), typedObj.ID, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func deleteByCosmosID(ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString, cosmosID string) error {
	_, err := containerClient.DeleteItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosID, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}
