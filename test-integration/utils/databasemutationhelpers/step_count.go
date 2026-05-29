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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

type countStep[InternalAPIType any] struct {
	stepID        StepID
	key           CosmosCRUDKey
	expectedCount int
}

func newCountStep[InternalAPIType any](stepID StepID, stepDir fs.FS) (*countStep[InternalAPIType], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read 00-key.json: %w", err)
	}
	var key CosmosCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal 00-key.json: %w", err)
	}

	countBytes, err := fs.ReadFile(stepDir, "expected-count.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected-count.json: %w", err)
	}
	var expectedCount int
	if err := json.Unmarshal(countBytes, &expectedCount); err != nil {
		return nil, fmt.Errorf("failed to unmarshal expected-count.json: %w", err)
	}

	return &countStep[InternalAPIType]{
		stepID:        stepID,
		key:           key,
		expectedCount: expectedCount,
	}, nil
}

var _ IntegrationTestStep = &countStep[any]{}

func (c *countStep[InternalAPIType]) StepID() StepID {
	return c.stepID
}

func (c *countStep[InternalAPIType]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType](t, stepInput.ResourcesDBClient, c.key.ParentResourceID, c.key.ResourceType.ResourceType)
	actualCount, err := resourceCRUDClient.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, c.expectedCount, actualCount, "unexpected count")
}
