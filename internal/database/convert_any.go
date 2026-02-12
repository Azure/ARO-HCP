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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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

	case *Controller:
		internalObj, err = CosmosToInternalController(cosmosObj)

	case *Operation:
		internalObj, err = CosmosToInternalOperation(cosmosObj)

	case *Subscription:
		internalObj, err = CosmosToInternalSubscription(cosmosObj)

	case *TypedDocument:
		var expectedObj InternalAPIType
		switch castObj := any(expectedObj).(type) {
		case TypedDocument:
			if cosmosObj.ResourceID != nil {
				return any(cosmosObj).(*InternalAPIType), nil
			}

			// fill in the new ResourceID field for old data that is missing it. This means we didn't migrate something.
			// this will happen frequently when the new backend is running against an old frontend.
			resourceIDFromOldCosmosID, err := oldCosmosIDToResourceID(cosmosObj.ID)
			if err != nil {
				return nil, utils.TrackError(fmt.Errorf("expected old cosmosID and got %q", castObj.ID))
			}
			cosmosObj.ResourceID = resourceIDFromOldCosmosID

			return any(cosmosObj).(*InternalAPIType), nil
		default:
			return nil, fmt.Errorf("unexpected return type: %T", castObj)
		}

	case *GenericDocument[InternalAPIType]:
		internalObj, err = CosmosGenericToInternal[InternalAPIType](cosmosObj)

	default:
		return nil, fmt.Errorf("unknown type %T", cosmosObj)
	}

	if err != nil {
		return nil, utils.TrackError(err)
	}
	castInternalObj, ok := internalObj.(*InternalAPIType)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement *InternalAPIType interface", internalObj)
	}

	return castInternalObj, nil
}

func oldCosmosIDToResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(strings.ReplaceAll(resourceID, "|", "/"))
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

	case *api.Controller:
		cosmosObj, err = InternalToCosmosController(internalObj)

	case *api.Operation:
		cosmosObj, err = InternalToCosmosOperation(internalObj)

	case *arm.Subscription:
		cosmosObj, err = InternalToCosmosSubscription(internalObj)

	case *TypedDocument:
		var expectedObj CosmosAPIType
		switch castObj := any(expectedObj).(type) {
		case TypedDocument:
			return any(internalObj).(*CosmosAPIType), nil
		default:
			return nil, fmt.Errorf("unexpected return type: %T", castObj)
		}

	default:
		cosmosObj, err = InternalToCosmosGeneric[InternalAPIType](obj)
	}

	if err != nil {
		return nil, utils.TrackError(err)
	}
	castCosmosObj, ok := cosmosObj.(*CosmosAPIType)
	if !ok {
		var o *CosmosAPIType
		return nil, fmt.Errorf("type %T does not implement *CosmosAPIType interface: %T", cosmosObj, o)
	}

	return castCosmosObj, nil
}
