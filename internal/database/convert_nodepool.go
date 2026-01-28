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
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func InternalToCosmosNodePool(internalObj *api.HCPOpenShiftClusterNodePool) (*NodePool, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &NodePool{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().CosmosUID,
			},
			PartitionKey: strings.ToLower(internalObj.ID.SubscriptionID),
			ResourceType: internalObj.ID.ResourceType.String(),
		},
		NodePoolProperties: NodePoolProperties{
			ResourceDocument: &ResourceDocument{
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
	cosmosObj.IntermediateResourceDoc = cosmosObj.ResourceDocument

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
	resourceDoc := cosmosObj.ResourceDocument
	if resourceDoc == nil {
		resourceDoc = cosmosObj.IntermediateResourceDoc
	}
	if resourceDoc == nil {
		return nil, fmt.Errorf("resource document cannot be nil")
	}

	tempInternalAPI := cosmosObj.InternalState.InternalAPI
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the ResourceDocument, so we need to restore that data
	internalObj.TrackedResource = arm.TrackedResource{
		Resource: arm.Resource{
			ID:         resourceDoc.ResourceID,
			Name:       resourceDoc.ResourceID.Name,
			Type:       resourceDoc.ResourceID.ResourceType.String(),
			SystemData: resourceDoc.SystemData,
		},
		Location: cosmosObj.InternalState.InternalAPI.Location,
		Tags:     resourceDoc.Tags,
	}
	internalObj.Identity = toInternalIdentity(resourceDoc.Identity)
	internalObj.Properties.ProvisioningState = resourceDoc.ProvisioningState
	internalObj.SystemData = resourceDoc.SystemData
	internalObj.Tags = copyTags(resourceDoc.Tags)
	internalObj.ServiceProviderProperties.CosmosUID = cosmosObj.ID
	internalObj.ServiceProviderProperties.ClusterServiceID = resourceDoc.InternalID
	internalObj.ServiceProviderProperties.ActiveOperationID = resourceDoc.ActiveOperationID

	return internalObj, nil
}
