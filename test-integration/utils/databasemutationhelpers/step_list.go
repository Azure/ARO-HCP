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
)

type listStep[InternalAPIType any] struct {
	stepID StepID
	key    CosmosCRUDKey

	expectedResources []*InternalAPIType
	expectedFilenames []string
}

func newListStep[InternalAPIType any](stepID StepID, stepDir fs.FS) (*listStep[InternalAPIType], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, expectedFilenames, err := readResourcesAndFilenamesInDir[InternalAPIType](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &listStep[InternalAPIType]{
		stepID:            stepID,
		key:               key,
		expectedResources: expectedResources,
		expectedFilenames: expectedFilenames,
	}, nil
}

var _ IntegrationTestStep = &listStep[any]{}

func (l *listStep[InternalAPIType]) StepID() StepID {
	return l.stepID
}

func (l *listStep[InternalAPIType]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType](t, stepInput.ResourcesDBClient, l.key.ParentResourceID, l.key.ResourceType.ResourceType)
	actualResourcesIterator, err := resourceCRUDClient.List(ctx, nil)
	require.NoError(t, err)

	actualResources := []*InternalAPIType{}
	for _, actual := range actualResourcesIterator.Items(ctx) {
		actualResources = append(actualResources, actual)
	}
	require.NoError(t, actualResourcesIterator.GetError())

	verifyOrUpdateList(t, l.stepID, toAnySlice(l.expectedResources), l.expectedFilenames, toAnySlice(actualResources), ResourceName)
}
