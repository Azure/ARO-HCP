// Copyright 2026 Microsoft Corporation
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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	metaapi "github.com/Azure/ARO-HCP/internal/apis/meta"
)

// serializeKubeApplierItem mirrors serializeItem but validates the partition key
// against the *Desire's spec.managementCluster instead of the resourceID's
// subscriptionID. The two never match for kube-applier objects.
func serializeKubeApplierItem[InternalAPIType, CosmosAPIType any](
	newObj *InternalAPIType,
) (*metaapi.CosmosMetadata, []byte, error) {
	cosmosPersistable, ok := any(newObj).(metaapi.CosmosPersistable)
	if !ok {
		return nil, nil, fmt.Errorf("type %T does not implement CosmosPersistable interface", newObj)
	}
	mgmtAccessor, ok := any(newObj).(kubeapplierapi.ManagementClusterAccessor)
	if !ok {
		return nil, nil, fmt.Errorf("type %T does not implement ManagementClusterAccessor", newObj)
	}
	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosUID := cosmosData.GetCosmosUID()
	if len(cosmosUID) == 0 {
		return nil, nil, fmt.Errorf("no cosmos id found in object")
	}
	if !strings.EqualFold(cosmosUID, strings.ToLower(cosmosUID)) {
		return nil, nil, fmt.Errorf("invalid cosmos id found in object")
	}
	if len(mgmtAccessor.GetManagementCluster()) == 0 {
		return nil, nil, fmt.Errorf("kube-applier object %T is missing spec.managementCluster", newObj)
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

// kubeApplierPartitionKey returns the lowercased management cluster name from a *Desire.
func kubeApplierPartitionKey[InternalAPIType any](newObj *InternalAPIType) (string, error) {
	mgmtAccessor, ok := any(newObj).(kubeapplierapi.ManagementClusterAccessor)
	if !ok {
		return "", fmt.Errorf("type %T does not implement ManagementClusterAccessor", newObj)
	}
	mgmt := strings.ToLower(mgmtAccessor.GetManagementCluster())
	if len(mgmt) == 0 {
		return "", fmt.Errorf("kube-applier object %T is missing spec.managementCluster", newObj)
	}
	return mgmt, nil
}

func createKubeApplier[InternalAPIType, CosmosAPIType any](
	ctx context.Context,
	containerClient *azcosmos.ContainerClient,
	partitionKeyString string,
	newObj *InternalAPIType,
	opts *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeKubeApplierItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	objMgmt, err := kubeApplierPartitionKey(newObj)
	if err != nil {
		return nil, err
	}
	if partitionKeyString != objMgmt {
		return nil, fmt.Errorf(
			"item management cluster does not match partition key: %q vs %q",
			objMgmt, partitionKeyString,
		)
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

func replaceKubeApplier[InternalAPIType, CosmosAPIType any](
	ctx context.Context,
	containerClient *azcosmos.ContainerClient,
	partitionKeyString string,
	newObj *InternalAPIType,
	opts *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return nil, fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeKubeApplierItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, err
	}
	objMgmt, err := kubeApplierPartitionKey(newObj)
	if err != nil {
		return nil, err
	}
	if partitionKeyString != objMgmt {
		return nil, fmt.Errorf(
			"item management cluster does not match partition key: %q vs %q",
			objMgmt, partitionKeyString,
		)
	}

	if opts == nil {
		opts = &azcosmos.ItemOptions{}
	}
	if len(cosmosMetadata.CosmosETag) > 0 {
		opts.IfMatchEtag = &cosmosMetadata.CosmosETag
	}
	opts.EnableContentResponseOnWrite = true

	responseItem, err := containerClient.ReplaceItem(
		ctx, azcosmos.NewPartitionKeyString(partitionKeyString), cosmosMetadata.GetCosmosUID(), data, opts,
	)
	if err != nil {
		return nil, err
	}

	return responseItemToInternalObj[InternalAPIType, CosmosAPIType](ctx, cosmosMetadata.GetCosmosUID(), responseItem)
}

func addKubeApplierCreateToTransaction[InternalAPIType, CosmosAPIType any](
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	partitionKeyString := transaction.GetPartitionKey()
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return "", fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeKubeApplierItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}
	objMgmt, err := kubeApplierPartitionKey(newObj)
	if err != nil {
		return "", err
	}
	if partitionKeyString != objMgmt {
		return "", fmt.Errorf(
			"item management cluster does not match partition key: %q vs %q",
			objMgmt, partitionKeyString,
		)
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

func addKubeApplierReplaceToTransaction[InternalAPIType, CosmosAPIType any](
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	partitionKeyString := transaction.GetPartitionKey()
	if strings.ToLower(partitionKeyString) != partitionKeyString {
		return "", fmt.Errorf("partitionKeyString must be lowercase, not: %q", partitionKeyString)
	}
	cosmosMetadata, data, err := serializeKubeApplierItem[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", err
	}
	objMgmt, err := kubeApplierPartitionKey(newObj)
	if err != nil {
		return "", err
	}
	if partitionKeyString != objMgmt {
		return "", fmt.Errorf(
			"item management cluster does not match partition key: %q vs %q",
			objMgmt, partitionKeyString,
		)
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
	if len(cosmosMetadata.CosmosETag) > 0 {
		opts.IfMatchETag = &cosmosMetadata.CosmosETag
	}

	transaction.AddStep(
		transactionDetails,
		func(b *azcosmos.TransactionalBatch) (string, error) {
			b.ReplaceItem(cosmosMetadata.GetCosmosUID(), data, opts)
			return cosmosMetadata.GetCosmosUID(), nil
		},
	)

	return cosmosMetadata.GetCosmosUID(), nil
}
