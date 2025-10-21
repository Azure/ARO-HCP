package database

import (
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func InternalToCosmosCluster(internalObj *api.HCPOpenShiftCluster) (*HCPCluster, error) {
	if internalObj == nil {
		return nil, nil
	}

	resourceID, err := azcorearm.ParseResourceID(internalObj.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID '%s': %w", internalObj.ID, err)
	}

	cosmosObj := &HCPCluster{
		TypedDocument: TypedDocument{},
		HCPClusterProperties: HCPClusterProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
				// TODO
				//InternalID:        ocm.InternalID{},
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
	cosmosObj.InternalState.InternalAPI.Properties.ProvisioningState = ""
	cosmosObj.InternalState.InternalAPI.SystemData = nil
	cosmosObj.InternalState.InternalAPI.Tags = nil

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
	internalObj.Properties.ProvisioningState = cosmosObj.ProvisioningState
	internalObj.SystemData = cosmosObj.SystemData
	internalObj.Tags = copyTags(cosmosObj.Tags)

	return internalObj, nil
}
