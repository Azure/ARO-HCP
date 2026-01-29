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
			ResourceType: internalObj.ResourceID.ResourceType.String(),
		},
		ControllerProperties: ControllerProperties{
			Controller:                 *internalObj,
			OldControllerSerialization: internalObj,
		},
	}

	// some pieces of data conflict with standard fields.  We may evolve over time, but for now avoid persisting those.

	return cosmosObj, nil
}

func CosmosToInternalController(cosmosObj *Controller) (*api.Controller, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	// if the old controller serialization is nil, then we return the new only
	if cosmosObj.ControllerProperties.OldControllerSerialization == nil {
		tempInternalAPI := cosmosObj.ControllerProperties.Controller

		return &tempInternalAPI, nil
	}

	// if we have an old controller serialization, then we need to honor that because if we have upgraded to new,
	// then rolledback, updated some controllers, then upgraded to new again, the content in old will be newer.
	// the Content in new is never updated independent of updating old

	tempInternalAPI := *cosmosObj.ControllerProperties.OldControllerSerialization
	// this is ok and necessary because the resourceID was always stored, it was simply stored during conversion before and now it is
	// stored in the json compatible api.Controller
	tempInternalAPI.ResourceID = cosmosObj.ControllerProperties.ResourceID
	tempInternalAPI.CosmosMetadata = api.CosmosMetadata{
		ResourceID: cosmosObj.ControllerProperties.ResourceID,
	}

	// some pieces of data are stored on the BaseDocument, so we need to restore that data

	return &tempInternalAPI, nil
}
