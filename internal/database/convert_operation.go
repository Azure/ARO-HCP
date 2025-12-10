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

func InternalToCosmosOperation(internalObj *api.Operation) (*Operation, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &Operation{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.CosmosUID,
			},
			PartitionKey: strings.ToLower(internalObj.ExternalID.SubscriptionID),
			ResourceType: internalObj.ComputeLogicalResourceID().ResourceType.String(),
		},
		OperationProperties: *internalObj,
	}

	// some pieces of data conflict with standard fields.  We may evolve over time, but for now avoid persisting those.
	cosmosObj.OperationProperties.CosmosUID = ""

	return cosmosObj, nil
}

func CosmosToInternalOperation(cosmosObj *Operation) (*api.Operation, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	tempInternalAPI := cosmosObj.OperationProperties
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the BaseDocument, so we need to restore that data
	internalObj.CosmosUID = cosmosObj.ID

	return internalObj, nil
}
