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

type ResourceKey struct {
	ResourceID string `json:"resourceId"`
}

type httpGetStep struct {
	stepID StepID
	key    ResourceKey

	expectedResource map[string]any
	expectedError    string
}

func newHTTPGetStep(stepID StepID, stepDir fs.FS) (*httpGetStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	var expectedResource map[string]any
	expectedResources, err := readResourcesInDir[map[string]any](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}
	switch len(expectedResources) {
	case 0:
	case 1:
		expectedResource = *expectedResources[0]
	default:
		return nil, fmt.Errorf("cannot expect more than one resource")
	}

	if len(expectedError) == 0 && expectedResource == nil {
		return nil, fmt.Errorf("must expect either error and value")
	}

	return &httpGetStep{
		stepID:           stepID,
		key:              key,
		expectedResource: expectedResource,
		expectedError:    expectedError,
	}, nil
}

var _ IntegrationTestStep = &httpGetStep{}

func (l *httpGetStep) StepID() StepID {
	return l.stepID
}

func (l *httpGetStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := stepInput.HTTPTestAccessor(l.key)
	actual, err := accessor.Get(ctx, l.key.ResourceID)
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

	if diff, equals := ResourceInstanceEquals(t, l.expectedResource, actual); !equals {
		t.Logf("actual:\n%v", stringifyResource(actual))
		t.Error(diff)
	}
}
