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
	"strings"

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

	cosmosObj := &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(metadata.GetResourceID().SubscriptionID),
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
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
	// Legacy documents predating the InstanceVersion field land here with
	// the zero value. Treat that as "version 1" so a subsequent Get → modify
	// → Replace path round-trips without tripping PrepareForReplace, which
	// only allows InstanceVersion==0 to flag fresh-built docs the caller
	// forgot to deep-copy from the existing record.
	if cosmosData.InstanceVersion == 0 {
		cosmosData.InstanceVersion = 1
	}

	if defaulter, ok := ret.(Defaulter); ok {
		defaulter.EnsureDefaults()
	}

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

type Defaulter interface {
	EnsureDefaults()
}
