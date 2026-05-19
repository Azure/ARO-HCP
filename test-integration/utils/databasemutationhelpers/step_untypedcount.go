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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type untypedCountStep struct {
	stepID        StepID
	key           UntypedCRUDKey
	expectedCount int
	recursive     bool
}

func newUntypedCountStep(stepID StepID, stepDir fs.FS) (*untypedCountStep, error) {
	return newUntypedCountStepInternal(stepID, stepDir, false)
}

func newUntypedCountRecursiveStep(stepID StepID, stepDir fs.FS) (*untypedCountStep, error) {
	return newUntypedCountStepInternal(stepID, stepDir, true)
}

func newUntypedCountStepInternal(stepID StepID, stepDir fs.FS, recursive bool) (*untypedCountStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read 00-key.json: %w", err)
	}
	var key UntypedCRUDKey
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

	return &untypedCountStep{
		stepID:        stepID,
		key:           key,
		expectedCount: expectedCount,
		recursive:     recursive,
	}, nil
}

var _ IntegrationTestStep = &untypedCountStep{}

func (c *untypedCountStep) StepID() StepID {
	return c.stepID
}

func (c *untypedCountStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	untypedCRUD, err := stepInput.ResourcesDBClient.UntypedCRUD(*c.key.ParentResourceID)
	require.NoError(t, err)
	for _, childKey := range c.key.Descendents {
		childResourceType, err := azcorearm.ParseResourceType(childKey.ResourceType)
		require.NoError(t, err)
		untypedCRUD, err = untypedCRUD.Child(childResourceType, childKey.ResourceName)
		require.NoError(t, err)
	}

	var actualCount int
	if c.recursive {
		actualCount, err = untypedCRUD.CountRecursive(ctx)
	} else {
		actualCount, err = untypedCRUD.Count(ctx)
	}
	require.NoError(t, err)
	require.Equal(t, c.expectedCount, actualCount, "unexpected count")
}
