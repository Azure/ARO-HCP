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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func InternalToCosmosExternalAuth(internalObj *api.HCPOpenShiftClusterExternalAuth) (*ExternalAuth, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &ExternalAuth{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.ServiceProviderProperties.CosmosUID,
			},
			PartitionKey: internalObj.ID.SubscriptionID,
			ResourceType: internalObj.ID.ResourceType.String(),
		},
		ExternalAuthProperties: ExternalAuthProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: internalObj.ID,
				InternalID: internalObj.ServiceProviderProperties.ClusterServiceID,
				// TODO
				//ActiveOperationID: "",
				ProvisioningState: internalObj.Properties.ProvisioningState,
				Identity:          nil,
				SystemData:        internalObj.SystemData,
				Tags:              nil,
			},
			InternalState: ExternalAuthInternalState{
				InternalAPI: *internalObj,
			},
		},
	}

	// some pieces of data in the internalExternalAuth conflict with ResourceDocument fields.  We may evolve over time, but for
	// now avoid persisting those.
	cosmosObj.InternalState.InternalAPI.ProxyResource = arm.ProxyResource{}
	cosmosObj.InternalState.InternalAPI.Properties.ProvisioningState = ""
	cosmosObj.InternalState.InternalAPI.SystemData = nil
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.CosmosUID = ""
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.ClusterServiceID = ocm.InternalID{}

	return cosmosObj, nil
}

func CosmosToInternalExternalAuth(cosmosObj *ExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	tempInternalAPI := cosmosObj.InternalState.InternalAPI
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the ResourceDocument, so we need to restore that data
	internalObj.ProxyResource = arm.ProxyResource{
		Resource: arm.Resource{
			ID:         cosmosObj.ResourceID,
			Name:       cosmosObj.ResourceID.Name,
			Type:       cosmosObj.ResourceID.ResourceType.String(),
			SystemData: cosmosObj.SystemData,
		},
	}
	internalObj.Properties.ProvisioningState = cosmosObj.ProvisioningState
	internalObj.SystemData = cosmosObj.SystemData
	internalObj.ServiceProviderProperties.CosmosUID = cosmosObj.ID
	internalObj.ServiceProviderProperties.ClusterServiceID = cosmosObj.InternalID

	return internalObj, nil
}
