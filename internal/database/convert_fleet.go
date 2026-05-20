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
)

// InternalToCosmosFleet wraps a fleet resource in a GenericDocument envelope whose
// partitionKey is the top-level ancestor resource name rather than the subscription ID.
// Once https://github.com/Azure/ARO-HCP/pull/5094 lands, this func becomes obsolete
// and we can use InternalToCosmosGeneric instead.
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

	partitionKey := topLevelResourceName(metadata.GetResourceID())
	if len(partitionKey) == 0 {
		return nil, fmt.Errorf("fleet object %T has no top-level resource name in its resource ID", internalObj)
	}

	return &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(partitionKey),
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}, nil
}
