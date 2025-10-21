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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func InternalToCosmosCluster(internalObj *api.HCPOpenShiftCluster) (*HCPCluster, error) {
	if internalObj == nil {
		return nil, nil
	}

	resourceID, err := azcorearm.ParseResourceID(internalObj.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID '%s': %w", internalObj.ID, err)
	}
	clusterServiceID, err := ocm.NewInternalID(internalObj.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cluster service ID '%s': %w", internalObj.ServiceProviderProperties.ClusterServiceID, err)
	}

	cosmosObj := &HCPCluster{
		TypedDocument: TypedDocument{},
		HCPClusterProperties: HCPClusterProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
				InternalID: clusterServiceID,
				// TODO
				//ActiveOperationID: "",
				ProvisioningState: internalObj.Properties.ProvisioningState,
				Identity:          toCosmosIdentity(internalObj.Identity),
				SystemData:        internalObj.SystemData,
				Tags:              copyTags(internalObj.Tags),
			},
			InternalState: ClusterInternalState{
				InternalAPI: *internalObj,
			},
		},
	}

	// some pieces of data in the internalCluster conflict with ResourceDocument fields.  We may evolve over time, but for
	// now avoid persisting those.
	cosmosObj.InternalState.InternalAPI.TrackedResource = arm.TrackedResource{
		Location: internalObj.Location, // this is the only TrackedResource value not present elsewhere in ResourceDcoument
	}
	cosmosObj.InternalState.InternalAPI.Identity = nil
	cosmosObj.InternalState.InternalAPI.SystemData = nil
	cosmosObj.InternalState.InternalAPI.Tags = nil
	cosmosObj.InternalState.InternalAPI.Properties.ProvisioningState = ""
	cosmosObj.InternalState.InternalAPI.ServiceProviderProperties.ClusterServiceID = ""

	// This is not the place for validation, but during such a transition we need to ensure we fail quickly and certainly
	// This flow will eventually be called when we replace the write path and we must always have a value.
	if len(cosmosObj.InternalID.String()) == 0 {
		panic("Developer Error: InternalID is required")
	}

	return cosmosObj, nil
}

func toCosmosIdentity(src *arm.ManagedServiceIdentity) *arm.ManagedServiceIdentity {
	if src == nil {
		return nil
	}
	tempIdentity := *src
	// we only keep the keys of the UserAssignedIdentities.
	// the values are looked up on azure somehow on demand
	if src.UserAssignedIdentities != nil {
		tempIdentity.UserAssignedIdentities = map[string]*arm.UserAssignedIdentity{}
		for k := range src.UserAssignedIdentities {
			tempIdentity.UserAssignedIdentities[k] = nil
		}
	}
	return &tempIdentity
}

func toInternalIdentity(src *arm.ManagedServiceIdentity) *arm.ManagedServiceIdentity {
	if src == nil {
		return nil
	}

	// at this point we still haven't restored the UserAssignedIdentities values, only the keys. The values are looked up on azure somehow in the frontend
	// this means that backend reads lack this data
	tempIdentity := *src
	return &tempIdentity
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

	tempInternalAPI := cosmosObj.InternalState.InternalAPI
	internalObj := &tempInternalAPI

	// some pieces of data are stored on the ResourceDocument, so we need to restore that data
	internalObj.TrackedResource = arm.TrackedResource{
		Resource: arm.Resource{
			ID:         cosmosObj.ResourceID.String(),
			Name:       cosmosObj.ResourceID.Name,
			Type:       cosmosObj.ResourceID.ResourceType.String(),
			SystemData: cosmosObj.SystemData,
		},
		Location: cosmosObj.InternalState.InternalAPI.Location,
		Tags:     cosmosObj.Tags,
	}
	internalObj.Identity = toInternalIdentity(cosmosObj.Identity)
	internalObj.SystemData = cosmosObj.SystemData
	internalObj.Tags = copyTags(cosmosObj.Tags)
	internalObj.Properties.ProvisioningState = cosmosObj.ProvisioningState
	internalObj.ServiceProviderProperties.ClusterServiceID = cosmosObj.InternalID.String()

	// This is not the place for validation, but during such a transition we need to ensure we fail quickly and certainly
	// This flow happens when reading both old and new data.  The old data should *always* have the internalID set
	if len(internalObj.ServiceProviderProperties.ClusterServiceID) == 0 {
		panic("Developer Error: InternalID is required")
	}

	return internalObj, nil
}
