// Copyright 2026 Microsoft Corporation
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
	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

// InternalToCosmosFleet wraps a fleet resource in a GenericDocument envelope whose
// partitionKey is the stamp identifier rather than the subscription ID.
// The fleet container is partitioned this way so that fleet management
// operations are scoped to a single management cluster stamp.
func InternalToCosmosFleet[InternalAPIType any](
	internalObj *InternalAPIType,
) (*GenericDocument[InternalAPIType], error) {
	if internalObj == nil {
		return nil, nil
	}

	metadata, ok := any(internalObj).(arm.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an arm.CosmosMetadataAccessor: %T", internalObj)
	}
	stampAccessor, ok := any(internalObj).(fleet.StampAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be a fleet.StampAccessor: %T", internalObj)
	}
	stampIdentifier := stampAccessor.GetStampIdentifier()
	if len(stampIdentifier) == 0 {
		return nil, fmt.Errorf("fleet object %T is missing spec.stampIdentifier", internalObj)
	}

	return &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(stampIdentifier),
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}, nil
}
