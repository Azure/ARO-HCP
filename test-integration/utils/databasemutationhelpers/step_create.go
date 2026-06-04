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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type createStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] struct {
	stepID StepID
	key    CosmosCRUDKey

	resources []*InternalAPIType
}

func newCreateStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]](stepID StepID, stepDir fs.FS) (*createStep[InternalAPIType, InternalAPITypePointer], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	resources, err := readResourcesInDir[InternalAPIType](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &createStep[InternalAPIType, InternalAPITypePointer]{
		stepID:    stepID,
		key:       key,
		resources: resources,
	}, nil
}

func (l *createStep[InternalAPIType, InternalAPITypePointer]) StepID() StepID {
	return l.stepID
}

func (l *createStep[InternalAPIType, InternalAPITypePointer]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType, InternalAPITypePointer](t, stepInput.ResourcesDBClient, l.key.ParentResourceID, l.key.ResourceType.ResourceType)

	for _, resource := range l.resources {
		_, err := resourceCRUDClient.Create(ctx, resource, nil)
		require.NoError(t, err, "failed to create controller")
	}
}
