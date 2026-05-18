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

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func InternalToCosmosNodePool(internalObj *api.HCPOpenShiftClusterNodePool) (*NodePool, error) {
	if internalObj == nil {
		return nil, nil
	}

	cosmosObj := &NodePool{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(internalObj.ID.SubscriptionID),
			ResourceID:   internalObj.ID,
			ResourceType: internalObj.ID.ResourceType.String(),
		},
		NodePoolProperties: NodePoolProperties{
			HCPOpenShiftClusterNodePool: *internalObj,
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: internalObj.ID,
			},
			IntermediateResourceDoc: &ResourceDocument{
				ResourceID:        internalObj.ID,
				InternalID:        ptr.Deref(internalObj.ServiceProviderProperties.ClusterServiceID, api.InternalID{}),
				ActiveOperationID: internalObj.ServiceProviderProperties.ActiveOperationID,
				ProvisioningState: internalObj.Properties.ProvisioningState,
				Identity:          internalObj.Identity.DeepCopy(),
				SystemData:        internalObj.SystemData,
				Tags:              copyTags(internalObj.Tags),
			},
			InternalState: NodePoolInternalState{
				InternalAPI: *internalObj,
			},
		},
	}

	return cosmosObj, nil
}

func CosmosToInternalNodePool(cosmosObj *NodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	if cosmosObj == nil {
		return nil, nil
	}
	resourceDoc := cosmosObj.IntermediateResourceDoc
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
	// we carry over the CosmosETag from the cosmos object to the internal object into a
	// temporary field until we have inlined and serialized CosmosMetadata in
	// HCPOpenShiftClusterNodePool.
	internalObj.CosmosETag = cosmosObj.BaseDocument.CosmosETag
	internalObj.Identity = resourceDoc.Identity.DeepCopy()
	internalObj.Properties.ProvisioningState = resourceDoc.ProvisioningState
	internalObj.SystemData = resourceDoc.SystemData
	internalObj.Tags = copyTags(resourceDoc.Tags)
	internalObj.ServiceProviderProperties.ExistingCosmosUID = cosmosObj.ID
	if len(resourceDoc.InternalID.String()) == 0 {
		// preserve the nil on read
		internalObj.ServiceProviderProperties.ClusterServiceID = nil
	} else {
		internalObj.ServiceProviderProperties.ClusterServiceID = &resourceDoc.InternalID
	}

	internalObj.ServiceProviderProperties.ActiveOperationID = resourceDoc.ActiveOperationID

	internalObj.EnsureDefaults()

	return internalObj, nil
}
