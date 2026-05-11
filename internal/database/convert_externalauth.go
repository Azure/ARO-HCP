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
)

func InternalToCosmosExternalAuth(internalObj *api.HCPOpenShiftClusterExternalAuth) (*ExternalAuth, error) {
	if internalObj == nil {
		return nil, nil
	}

	// CosmosMetadata.ResourceID is the canonical identifier for cosmos-side
	// concerns (partitioning, document UID, resource-type indexing). Use it
	// instead of the ProxyResource.ID, which is an ARM-surface concern.
	cosmosResourceID := internalObj.GetCosmosData().ResourceID
	if cosmosResourceID == nil {
		return nil, fmt.Errorf("internalObj is missing CosmosMetadata.ResourceID")
	}
	cosmosObj := &ExternalAuth{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(cosmosResourceID.SubscriptionID),
			ResourceID:   cosmosResourceID,
			ResourceType: cosmosResourceID.ResourceType.String(),
		},
		ExternalAuthProperties: ExternalAuthProperties{
			HCPOpenShiftClusterExternalAuth: *internalObj,
		},
	}

	return cosmosObj, nil
}

func CosmosToInternalExternalAuth(cosmosObj *ExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
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
