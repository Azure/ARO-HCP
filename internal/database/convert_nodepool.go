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
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func InternalToCosmosNodePool(internalObj *api.HCPOpenShiftClusterNodePool) (*NodePool, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &NodePool{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.ServiceProviderProperties.CosmosUID,
			},
			PartitionKey: strings.ToLower(internalObj.ID.SubscriptionID),
			ResourceType: internalObj.ID.ResourceType.String(),
		},
		NodePoolProperties: NodePoolProperties{
			ResourceDocument: ResourceDocument{
				ResourceID:        internalObj.ID,
				InternalID:        internalObj.ServiceProviderProperties.ClusterServiceID,
				ActiveOperationID: internalObj.ServiceProviderProperties.ActiveOperationID,
				ProvisioningState: internalObj.Properties.ProvisioningState,
				Identity:          toCosmosIdentity(internalObj.Identity),
				SystemData:        internalObj.SystemData,
				Tags:              copyTags(internalObj.Tags),
			},
			InternalState: NodePoolInternalState{
				InternalAPI: *internalObj,
			},
		},
	}

	// some pieces of data in the internalNodePool conflict with ResourceDocument fields.  We may evolve over time, but for
	// now avoid persisting those.
	cosmosObj.InternalState.InternalAPI.TrackedResource = arm.TrackedResource{
		Location: internalObj.Location, // this is the only TrackedResource value not present elsewhere in ResourceDcoument
	}
	cosmosObj.InternalState.InternalAPI.Identity = nil
	cosmosObj.InternalState.InternalAPI.Properties.ProvisioningState = ""
	cosmosObj.InternalState.InternalAPI.SystemData = nil
	cosmosObj.InternalState.InternalAPI.Tags = nil
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.CosmosUID = ""
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.ClusterServiceID = ocm.InternalID{}
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.ActiveOperationID = ""

	return cosmosObj, nil
}

func CosmosToInternalNodePool(cosmosObj *NodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	tempInternalAPI := cosmosObj.InternalState.InternalAPI
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the ResourceDocument, so we need to restore that data
	internalObj.TrackedResource = arm.TrackedResource{
		Resource: arm.Resource{
			ID:         cosmosObj.ResourceID,
			Name:       cosmosObj.ResourceID.Name,
			Type:       cosmosObj.ResourceID.ResourceType.String(),
			SystemData: cosmosObj.SystemData,
		},
		Location: cosmosObj.InternalState.InternalAPI.Location,
		Tags:     cosmosObj.Tags,
	}
	internalObj.Identity = toInternalIdentity(cosmosObj.Identity)
	internalObj.Properties.ProvisioningState = cosmosObj.ProvisioningState
	internalObj.SystemData = cosmosObj.SystemData
	internalObj.Tags = copyTags(cosmosObj.Tags)
	internalObj.ServiceProviderProperties.CosmosUID = cosmosObj.ID
	internalObj.ServiceProviderProperties.ClusterServiceID = cosmosObj.InternalID
	internalObj.ServiceProviderProperties.ActiveOperationID = cosmosObj.ActiveOperationID

	return internalObj, nil
}
