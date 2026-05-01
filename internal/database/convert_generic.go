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
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const operationTimeToLive = 604800 // 7 days

func InternalToCosmosGeneric[InternalAPIType any](internalObj *InternalAPIType) (*GenericDocument[InternalAPIType], error) {
	if internalObj == nil {
		return nil, nil
	}

	metadata, ok := any(internalObj).(arm.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an arm.CosmosMetadataAccessor: %T", internalObj)
	}

	partitionKey := metadata.GetPartitionKey()
	cosmosObj := &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: partitionKey,
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}
	// Mirror the envelope's partitionKey into the inner cosmosMetadata copy so the on-disk
	// representation has both fields in sync. We mutate the value-copy in cosmosObj.Content
	// rather than the caller-supplied internalObj.
	if cm, ok := any(&cosmosObj.Content).(arm.CosmosMetadataAccessor); ok {
		cm.SetPartitionKey(partitionKey)
	}

	// this isn't pretty, but on balance it's a better choice so that we can share all the rest.
	switch any(internalObj).(type) {
	case *api.Operation:
		// TODO Add TTL to cosmosMetadata
		cosmosObj.TimeToLive = operationTimeToLive
	}

	return cosmosObj, nil
}

func CosmosGenericToInternal[InternalAPIType any](cosmosObj *GenericDocument[InternalAPIType]) (*InternalAPIType, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	ret, ok := any(&cosmosObj.Content).(arm.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an arm.CosmosMetadataAccessor: %T", cosmosObj)
	}
	cosmosData := ret.(arm.CosmosPersistable).GetCosmosData()
	cosmosData.ExistingCosmosUID = cosmosObj.ID
	ret.SetEtag(cosmosObj.CosmosETag)
	// Round-trip the envelope's partitionKey back into the metadata so callers
	// can read it without re-deriving from ResourceID.SubscriptionID.
	ret.SetPartitionKey(cosmosObj.PartitionKey)

	// this isn't pretty, but on balance it's a better choice so that we can share all the rest.
	switch castObj := any(ret).(type) {
	case *arm.Subscription:
		if castObj.CosmosMetadata.ResourceID == nil && castObj.ResourceID != nil {
			castObj.CosmosMetadata.ResourceID = castObj.ResourceID
		}
		if castObj.CosmosMetadata.ResourceID == nil && cosmosObj.ResourceID != nil {
			castObj.CosmosMetadata.ResourceID = cosmosObj.ResourceID
		}
		castObj.LastUpdated = cosmosObj.CosmosTimestamp
	case arm.Subscription:
		castObj.LastUpdated = cosmosObj.CosmosTimestamp
	}

	if ret.GetResourceID() == nil {
		if cosmosObj.ResourceID != nil {
			ret.SetResourceID(cosmosObj.ResourceID)
		} else {
			return nil, fmt.Errorf("internalObj is missing a resourceID: %T: %q", cosmosObj, cosmosObj.ID)
		}
	}

	return &cosmosObj.Content, nil
}
