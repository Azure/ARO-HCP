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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type deleteStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] struct {
	stepID        StepID
	key           CosmosItemKey
	expectedError string
}

func newDeleteStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]](stepID StepID, stepDir fs.FS) (*deleteStep[InternalAPIType, InternalAPITypePointer], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosItemKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	return &deleteStep[InternalAPIType, InternalAPITypePointer]{
		stepID:        stepID,
		key:           key,
		expectedError: expectedError,
	}, nil
}

func (l *deleteStep[InternalAPIType, InternalAPITypePointer]) StepID() StepID {
	return l.stepID
}

func (l *deleteStep[InternalAPIType, InternalAPITypePointer]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType, InternalAPITypePointer](t, stepInput.ResourcesDBClient, l.key.ResourceID.Parent, l.key.ResourceID.ResourceType)
	err := resourceCRUDClient.Delete(ctx, l.key.ResourceID.Name)
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

}
