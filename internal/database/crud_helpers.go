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

func responseItemToInternalObj[InternalAPIType, CosmosAPIType any](ctx context.Context, cosmosID string, responseItem azcosmos.ItemResponse) (*InternalAPIType, error) {
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

	return responseItemToInternalObj[InternalAPIType, CosmosAPIType](ctx, cosmosID, responseItem)
}

func get[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, completeResourceID *azcorearm.ResourceID) (*InternalAPIType, error) {
	// try to see if the cosmosID we've passed is also the exact resource ID.  If so, then return the value we got.
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
		query = "SELECT * FROM c WHERE LENGTH(c.resourceID) > 0"
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
		query += " AND STRINGEQUALS(c.resourceType, @resourceType, true)"
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

// asAccessor casts newObj to arm.CosmosMetadataAccessor and returns a clean
// error when the cast fails. The four CRUD entry points (create / replace /
// addCreate / addReplaceToTransaction) are generic over an unconstrained
// InternalAPIType so they can plug into the existing generic CRUD wrappers,
// and a pointer-to-type-parameter cannot satisfy a Go generic constraint
// like `interface { *T; arm.CosmosMetadataAccessor }`. We pay one runtime
// type assertion at the entry point instead.
func asAccessor[InternalAPIType any](newObj *InternalAPIType) (arm.CosmosMetadataAccessor, error) {
	accessor, ok := any(newObj).(arm.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement CosmosMetadataAccessor", newObj)
	}
	return accessor, nil
}

// PrepareForCreate sets InstanceVersion to 1 on the CosmosMetadata of newObj.
// All Create paths (in this package and in databasetesting) must call this
// before serializing the document so that a fresh insert starts the version
// counter at 1. The value is unconditionally overwritten so callers can't
// accidentally carry over a value from a prior Get.
func PrepareForCreate[InternalAPIType any](newObj *InternalAPIType) error {
	accessor, err := asAccessor(newObj)
	if err != nil {
		return err
	}
	if accessor.GetInstanceVersion() != 0 {
		return fmt.Errorf("create of %T requires InstanceVersion to be 0; refusing to overwrite existing value", newObj)
	}
	accessor.SetInstanceVersion(1)
	return nil
}

// PrepareForReplace enforces the two invariants of an update: every Replace
// must carry a CosmosETag (we refuse unconditional updates) and the on-disk
// InstanceVersion auto-increments. All Replace paths (in this package and in
// databasetesting) must call this before serializing the document.
func PrepareForReplace[InternalAPIType any](newObj *InternalAPIType) error {
	accessor, err := asAccessor(newObj)
	if err != nil {
		return err
	}
	if len(accessor.GetEtag()) == 0 {
		return fmt.Errorf("replace of %T requires a non-empty CosmosETag; refusing to perform an unconditional update", newObj)
	}
	if accessor.GetInstanceVersion() == 0 {
		return fmt.Errorf("replace of %T requires a non-zero InstanceVersion; refusing to perform update; DeepCopy the existing content to avoid overwrite", newObj)
	}
	accessor.SetInstanceVersion(accessor.GetInstanceVersion() + 1)
	return nil
}

// SerializeItem reads the partition key from newObj's CosmosMetadata and
// serializes the object to JSON. The caller is responsible for populating
// CosmosMetadata.PartitionKey ahead of time (via SetPartitionKey);
// SerializeItem refuses to write a document with an empty partition key
// because doing so silently corrupts the container.
func SerializeItem[InternalAPIType, CosmosAPIType any](newObj *InternalAPIType) (*arm.CosmosMetadata, []byte, error) {
	accessor, err := asAccessor(newObj)
	if err != nil {
		return nil, nil, err
	}
	cosmosData := accessor.(arm.CosmosPersistable).GetCosmosData()
	if len(accessor.GetPartitionKey()) == 0 {
		return nil, nil, fmt.Errorf("type %T has no PartitionKey on its CosmosMetadata; the CRUD layer must call SetPartitionKey before serializing", newObj)
	}
	if strings.ToLower(accessor.GetPartitionKey()) != accessor.GetPartitionKey() {
		return nil, nil, fmt.Errorf("%q must be lowercase", accessor.GetPartitionKey())
	}
	cosmosUID := accessor.GetCosmosUID()
	if len(cosmosUID) == 0 {
		return nil, nil, fmt.Errorf("no cosmos id found in object")
	}
	if !strings.EqualFold(cosmosUID, strings.ToLower(cosmosUID)) {
		return nil, nil, fmt.Errorf("invalid cosmos id found in object")
	}
	if cosmosUID != strings.ToLower(cosmosUID) {
		return nil, nil, fmt.Errorf("cosmos id must be lowercase: %q", cosmosUID)
	}
	if accessor.GetInstanceVersion() <= 0 {
		return nil, nil, fmt.Errorf("object InstanceVersion must be positive: %d", accessor.GetInstanceVersion())
	}

	cosmosObj, err := InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert internal object to Cosmos object: %w", err)
	}
	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Cosmos DB item for '%s': %w", accessor.GetResourceID(), err)
	}

	return cosmosData, data, nil
}

// objectPartitionKey returns the lowercased partition key carried on the
// object's CosmosMetadata. Returns an error when the value is unset — write
// helpers rely on the caller to have populated it via SetPartitionKey.
func objectPartitionKey[InternalAPIType any](newObj *InternalAPIType) (string, error) {
	accessor, err := asAccessor(newObj)
	if err != nil {
		return "", err
	}
	pk := accessor.GetPartitionKey()
	if len(pk) == 0 {
		return "", fmt.Errorf("type %T has no PartitionKey on its CosmosMetadata; the CRUD layer must call SetPartitionKey before this point", newObj)
	}
	return pk, nil
}

func addCreateToTransaction[InternalAPIType, CosmosAPIType any](ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	if err := PrepareForCreate(newObj); err != nil {
		return "", err
	}
	partitionKeyString, err := objectPartitionKey(newObj)
	if err != nil {
		return "", err
	}
	if txPK := transaction.GetPartitionKey(); txPK != partitionKeyString {
		return "", fmt.Errorf("object partition key %q does not match transaction partition key %q", partitionKeyString, txPK)
	}
	cosmosMetadata, data, err := SerializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
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
	if err := PrepareForReplace(newObj); err != nil {
		return "", err
	}
	cosmosMetadata, data, err := SerializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}
	partitionKeyString, err := objectPartitionKey(newObj)
	if err != nil {
		return "", err
	}
	if txPK := transaction.GetPartitionKey(); txPK != partitionKeyString {
		return "", fmt.Errorf("object partition key %q does not match transaction partition key %q", partitionKeyString, txPK)
	}
	transactionDetails := CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosMetadata.GetCosmosUID(),
		ResourceID: cosmosMetadata.ResourceID.String(),
		Etag:       cosmosMetadata.CosmosETag,
	}

	if opts == nil {
		opts = &azcosmos.TransactionalBatchItemOptions{}
	}
	opts.IfMatchETag = &cosmosMetadata.CosmosETag

	transaction.AddStep(
		transactionDetails,
		func(b *azcosmos.TransactionalBatch) (string, error) {
			b.ReplaceItem(cosmosMetadata.GetCosmosUID(), data, opts)
			return cosmosMetadata.GetCosmosUID(), nil
		},
	)

	return cosmosMetadata.GetCosmosUID(), nil
}

func create[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := PrepareForCreate(newObj); err != nil {
		return nil, err
	}
	cosmosMetadata, data, err := SerializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	partitionKeyString, err := objectPartitionKey(newObj)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	opts.EnableContentResponseOnWrite = true

	responseItem, err := containerClient.CreateItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), data, opts)
	if err != nil {
		return nil, err
	}

	return responseItemToInternalObj[InternalAPIType, CosmosAPIType](ctx, cosmosMetadata.GetCosmosUID(), responseItem)
}

func replace[InternalAPIType, CosmosAPIType any](ctx context.Context, containerClient *azcosmos.ContainerClient, newObj *InternalAPIType, opts *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := PrepareForReplace(newObj); err != nil {
		return nil, err
	}
	cosmosMetadata, data, err := SerializeItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	partitionKeyString, err := objectPartitionKey(newObj)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	opts.IfMatchEtag = &cosmosMetadata.CosmosETag
	opts.EnableContentResponseOnWrite = true

	responseItem, err := containerClient.ReplaceItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosMetadata.GetCosmosUID(), data, opts)
	if err != nil {
		return nil, err
	}

	return responseItemToInternalObj[InternalAPIType, CosmosAPIType](ctx, cosmosMetadata.GetCosmosUID(), responseItem)
}

func deleteResource(ctx context.Context, containerClient *azcosmos.ContainerClient, partitionKeyString string, resourceID *azcorearm.ResourceID) error {
	cosmosID, err := arm.ResourceIDToCosmosID(resourceID)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = containerClient.DeleteItem(ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosID, nil)
	if IsNotFoundError(err) {
		return nil
	}
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
