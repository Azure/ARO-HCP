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
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const operationTimeToLive = 604800 // 7 days

func InternalToCosmosOperation(internalObj *api.Operation) (*Operation, error) {
	if internalObj == nil {
		return nil, nil
	}
	if internalObj.OperationID == nil {
		return nil, fmt.Errorf("operation id cannot be nil")
	}
	if len(internalObj.OperationID.Name) == 0 {
		return nil, fmt.Errorf("operation id name cannot be empty")
	}

	cosmosObj := &Operation{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID:         internalObj.OperationID.Name,
				TimeToLive: operationTimeToLive,
			},
			PartitionKey: strings.ToLower(internalObj.ExternalID.SubscriptionID),
			ResourceType: api.OperationStatusResourceType.String(),
		},
		OperationProperties: *internalObj,
	}

	// some pieces of data conflict with standard fields.  We may evolve over time, but for now avoid persisting those.

	return cosmosObj, nil
}

func CosmosToInternalOperation(cosmosObj *Operation) (*api.Operation, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	tempInternalAPI := cosmosObj.OperationProperties
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the BaseDocument, so we need to restore that data
	if internalObj.OperationID == nil {
		var err error
		internalObj.OperationID, err = azcorearm.ParseResourceID(
			strings.ToLower(
				path.Join("/",
					"subscriptions", cosmosObj.PartitionKey,
					"providers", api.ProviderNamespace,
					"locations", arm.GetAzureLocation(),
					api.OperationStatusResourceTypeName,
					cosmosObj.ID)))
		if err != nil {
			return nil, fmt.Errorf("unable to create operationID for %q in %q: %w", cosmosObj.ID, cosmosObj.PartitionKey, err)
		}
	}

	return internalObj, nil
}
