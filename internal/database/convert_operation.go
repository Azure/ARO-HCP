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
				ID:         internalObj.GetCosmosData().CosmosUID,
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

	// old records don't serialize this, but we want all readers to be able to depend on it. We can derive it from the operationID
	// this ID does not include the location because doing so changes the resulting azcorearm.ParseResourceID().ResourceType to be
	// Microsoft.RedHatOpenShift/locations/hcpOperationStatuses.  This type is not compatible with the current cosmos storage and
	// nests in a way that doesn't match other types. Since our operationID.Name is a UID, this is still a globally unique
	// resourceID.
	if internalObj.ResourceID == nil {
		internalObj.ResourceID = api.Must(azcorearm.ParseResourceID(path.Join("/",
			"subscriptions", internalObj.ExternalID.SubscriptionID,
			"providers", api.ProviderNamespace,
			api.OperationStatusResourceTypeName, internalObj.OperationID.Name,
		)))
	}

	return internalObj, nil
}
