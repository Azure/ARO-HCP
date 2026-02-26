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

type httpListStep struct {
	stepID StepID
	key    ResourceKey

	expectedResources []*map[string]any
}

func newHTTPListStep(stepID StepID, stepDir fs.FS) (*httpListStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, err := readResourcesInDir[map[string]any](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &httpListStep{
		stepID:            stepID,
		key:               key,
		expectedResources: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &httpListStep{}

func (l *httpListStep) StepID() StepID {
	return l.stepID
}

func (l *httpListStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := stepInput.HTTPTestAccessor(l.key)
	actualResources, err := accessor.List(ctx, l.key.ResourceID)
	require.NoError(t, err)

	if len(l.expectedResources) != len(actualResources) {
		t.Logf("actual:\n%v", stringifyResource(actualResources))
	}

	require.Equal(t, len(l.expectedResources), len(actualResources), "unexpected number of resources")
	// all the expected must be present
	for i, expected := range l.expectedResources {
		found := false
		for _, actual := range actualResources {
			_, equals := ResourceInstanceEquals(t, expected, actual)
			if equals {
				found = true
				break
			}
		}
		if !found {
			t.Logf("actual:\n%v", stringifyResource(actualResources))
		}
		require.True(t, found, "expected resource not found: %d", i)
	}

	// all the actual must be expected
	for i, actual := range actualResources {
		found := false
		for _, expected := range l.expectedResources {
			_, equals := ResourceInstanceEquals(t, expected, actual)
			if equals {
				found = true
				break
			}
		}
		if !found {
			t.Logf("expected:\n%v", stringifyResource(l.expectedResources))
		}
		require.True(t, found, "actual resource not found: %d", i)
	}
}
