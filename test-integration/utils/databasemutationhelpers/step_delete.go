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
)

type CosmosDeleteKey struct {
	CosmosCRUDKey `json:",inline"`

	DeleteResourceName string `json:"deleteResourceName"`
}

type deleteStep[InternalAPIType any] struct {
	stepID      StepID
	key         CosmosDeleteKey
	specializer ResourceCRUDTestSpecializer[InternalAPIType]

	expectedError string
}

func newDeleteStep[InternalAPIType any](stepID StepID, specializer ResourceCRUDTestSpecializer[InternalAPIType], stepDir fs.FS) (*deleteStep[InternalAPIType], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosDeleteKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	return &deleteStep[InternalAPIType]{
		stepID:        stepID,
		key:           key,
		specializer:   specializer,
		expectedError: expectedError,
	}, nil
}

var _ IntegrationTestStep = &deleteStep[any]{}

func (l *deleteStep[InternalAPIType]) StepID() StepID {
	return l.stepID
}

func (l *deleteStep[InternalAPIType]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	controllerCRUDClient := l.specializer.ResourceCRUDFromKey(t, stepInput.CosmosContainer, l.key.CosmosCRUDKey)
	err := controllerCRUDClient.Delete(ctx, l.key.DeleteResourceName)
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

}
