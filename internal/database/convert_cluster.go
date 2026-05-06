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
)

func InternalToCosmosCluster(internalObj *api.HCPOpenShiftCluster) (*HCPCluster, error) {
	if internalObj == nil {
		return nil, nil
	}

	// CosmosMetadata.ResourceID is the canonical identifier for cosmos-side
	// concerns (partitioning, document UID, resource-type indexing). Use it
	// instead of the TrackedResource.ID, which is an ARM-surface concern.
	cosmosResourceID := internalObj.GetCosmosData().ResourceID
	if cosmosResourceID == nil {
		return nil, fmt.Errorf("internalObj is missing CosmosMetadata.ResourceID")
	}
	cosmosObj := &HCPCluster{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(cosmosResourceID.SubscriptionID),
			ResourceID:   cosmosResourceID,
			ResourceType: cosmosResourceID.ResourceType.String(),
		},
		HCPClusterProperties: HCPClusterProperties{
			HCPOpenShiftCluster: *internalObj,
			IntermediateResourceDoc: &ResourceDocument{
				ResourceID:        cosmosResourceID,
				InternalID:        ptr.Deref(internalObj.ServiceProviderProperties.ClusterServiceID, api.InternalID{}),
				ActiveOperationID: internalObj.ServiceProviderProperties.ActiveOperationID,
				ProvisioningState: internalObj.ServiceProviderProperties.ProvisioningState,
				Identity:          internalObj.Identity.DeepCopy(),
				SystemData:        internalObj.SystemData,
				Tags:              copyTags(internalObj.Tags),
			},
			InternalState: ClusterInternalState{
				InternalAPI: *internalObj,
			},
		},
	}

	cosmosObj.InternalState.InternalAPI.CosmosMetadata = api.CosmosMetadata{}

	return cosmosObj, nil
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	tags := map[string]string{}
	for k, v := range src {
		tags[k] = v
	}

	return tags
}

func CosmosToInternalCluster(cosmosObj *HCPCluster) (*api.HCPOpenShiftCluster, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	internalObj := cosmosObj.DeepCopy()
	internalObj.ExistingCosmosUID = cosmosObj.ID
	internalObj.SetEtag(cosmosObj.CosmosETag)
	if internalObj.GetResourceID() == nil {
		if cosmosObj.ResourceID != nil {
			internalObj.SetResourceID(cosmosObj.ResourceID)
		} else {
			return nil, fmt.Errorf("internalObj is missing a resourceID: %T: %q", cosmosObj, cosmosObj.ID)
		}
	}

	internalObj.EnsureDefaults()

	return internalObj, nil
}
