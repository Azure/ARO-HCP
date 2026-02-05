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
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
)

func InternalToCosmosController(internalObj *api.Controller) (*Controller, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &Controller{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(internalObj.ExternalID.SubscriptionID),
			ResourceID:   internalObj.CosmosMetadata.ResourceID,
			ResourceType: internalObj.ResourceID.ResourceType.String(),
		},
		ControllerProperties: ControllerProperties{
			Controller: *internalObj,
		},
	}

	return cosmosObj, nil
}

func CosmosToInternalController(cosmosObj *Controller) (*api.Controller, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	// some pieces of data are stored on the BaseDocument, so we need to restore that data
	internalObj := cosmosObj.ControllerProperties.Controller
	internalObj.CosmosMetadata.ExistingCosmosUID = cosmosObj.ID
	internalObj.SetEtag(cosmosObj.CosmosETag)

	return &internalObj, nil
}
