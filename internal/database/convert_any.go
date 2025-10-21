package database

import (
	"encoding/json"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ResourceDocumentToInternalAPI is convenient for old code that uses ResourceDocument and needs to get to the internalAPI
// this is very expensive while we transition
func ResourceDocumentToInternalAPI[InternalAPIType, CosmosAPIType any](src *ResourceDocument) (*InternalAPIType, error) {
	resourceDocumentJSON, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	fullDocument := &TypedDocument{
		Properties: resourceDocumentJSON,
	}
	fullDocumentJSON, err := json.Marshal(fullDocument)
	if err != nil {
		return nil, err
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(fullDocumentJSON, &cosmosObj); err != nil {
		return nil, err
	}

	return CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
}

func CosmosToInternal[InternalAPIType, CosmosAPIType any](obj *CosmosAPIType) (*InternalAPIType, error) {
	var internalObj any
	var err error
	switch cosmosObj := any(obj).(type) {
	case *ExternalAuth:
		internalObj, err = CosmosToInternalExternalAuth(cosmosObj)

	case *HCPCluster:
		internalObj, err = CosmosToInternalCluster(cosmosObj)

	case *NodePool:
		internalObj, err = CosmosToInternalNodePool(cosmosObj)

	default:
		return nil, fmt.Errorf("unknown type %T", cosmosObj)
	}

	if err != nil {
		return nil, err
	}
	castInternalObj, ok := internalObj.(*InternalAPIType)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement *InternalAPIType interface", internalObj)
	}

	return castInternalObj, nil
}

func InternalToCosmos[InternalAPIType, CosmosAPIType any](obj *InternalAPIType) (*CosmosAPIType, error) {
	var cosmosObj any
	var err error
	switch internalObj := any(obj).(type) {
	case *api.HCPOpenShiftClusterExternalAuth:
		cosmosObj, err = InternalToCosmosExternalAuth(internalObj)

	case *api.HCPOpenShiftCluster:
		cosmosObj, err = InternalToCosmosCluster(internalObj)

	case *api.HCPOpenShiftClusterNodePool:
		cosmosObj, err = InternalToCosmosNodePool(internalObj)

	default:
		return nil, fmt.Errorf("unknown type %T", internalObj)
	}

	if err != nil {
		return nil, err
	}
	castCosmosObj, ok := cosmosObj.(*CosmosAPIType)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement *InternalAPIType interface", cosmosObj)
	}

	return castCosmosObj, nil
}
