package database

import (
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func InternalToCosmosExternalAuth(internalObj *api.HCPOpenShiftClusterExternalAuth) (*ExternalAuth, error) {
	if internalObj == nil {
		return nil, nil
	}

	resourceID, err := azcorearm.ParseResourceID(internalObj.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID '%s': %w", internalObj.ID, err)
	}

	cosmosObj := &ExternalAuth{
		TypedDocument: TypedDocument{},
		ExternalAuthProperties: ExternalAuthProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
				// TODO
				//InternalID:        ocm.InternalID{},
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
			ID:         cosmosObj.ResourceID.String(),
			Name:       cosmosObj.ResourceID.Name,
			Type:       cosmosObj.ResourceID.ResourceType.String(),
			SystemData: cosmosObj.SystemData,
		},
	}
	internalObj.Properties.ProvisioningState = cosmosObj.ProvisioningState
	internalObj.SystemData = cosmosObj.SystemData

	return internalObj, nil
}
