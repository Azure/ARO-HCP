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

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func InternalToCosmosSubscription(internalObj *arm.Subscription) (*Subscription, error) {
	if internalObj == nil {
		return nil, nil
	}

	if len(internalObj.ResourceID.Name) == 0 {
		return nil, fmt.Errorf("invalid resource id: %q", internalObj.ResourceID.String())
	}

	cosmosObj := &Subscription{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: internalObj.GetCosmosData().GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(internalObj.ResourceID.Name),
			ResourceID:   internalObj.ResourceID,
			ResourceType: internalObj.ResourceID.ResourceType.String(),
		},
		InternalState: SubscriptionProperties{
			Subscription: *internalObj,
		},
	}

	// some pieces of data conflict with standard fields.  We may evolve over time, but for now avoid persisting those.

	return cosmosObj, nil
}

func CosmosToInternalSubscription(cosmosObj *Subscription) (*arm.Subscription, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	tempInternalAPI := cosmosObj.InternalState.Subscription
	internalObj := &tempInternalAPI

	// old records don't serialize this, but we want all readers to be able to depend on it.
	if internalObj.CosmosMetadata.ResourceID == nil {
		internalObj.CosmosMetadata.ResourceID = internalObj.ResourceID
	}
	internalObj.LastUpdated = cosmosObj.CosmosTimestamp

	return internalObj, nil
}
