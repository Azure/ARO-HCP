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
)

func InternalToCosmosGeneric[InternalAPIType any](internalObj *InternalAPIType) (*GenericDocument[InternalAPIType], error) {
	if internalObj == nil {
		return nil, nil
	}

	metadata, ok := any(internalObj).(api.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an api.CosmosMetadataAccessor: %T", internalObj)
	}

	cosmosID, err := api.ResourceIDToCosmosID(metadata.GetResourceID())
	if err != nil {
		return nil, err
	}

	cosmosObj := &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: cosmosID,
			},
			PartitionKey: strings.ToLower(metadata.GetResourceID().SubscriptionID),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}

	return cosmosObj, nil
}

func CosmosGenericToInternal[InternalAPIType any](cosmosObj *GenericDocument[InternalAPIType]) (*InternalAPIType, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	ret, ok := any(&cosmosObj.Content).(api.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an api.CosmosMetadataAccessor: %T", cosmosObj)
	}
	ret.SetEtag(cosmosObj.CosmosETag)

	return &cosmosObj.Content, nil
}
