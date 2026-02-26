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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type httpPostStep struct {
	stepID StepID
	key    ResourceKey

	resources     [][]byte
	expectedError string
}

func newHTTPPostStep(stepID StepID, stepDir fs.FS) (*httpPostStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	resources, err := readRawBytesInDir(stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	return &httpPostStep{
		stepID:        stepID,
		key:           key,
		resources:     resources,
		expectedError: expectedError,
	}, nil
}

var _ IntegrationTestStep = &httpPostStep{}

func (l *httpPostStep) StepID() StepID {
	return l.stepID
}

func (l *httpPostStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := stepInput.HTTPTestAccessor(l.key)

	for _, resource := range l.resources {
		err := accessor.Post(ctx, l.key.ResourceID, resource)

		switch {
		case len(l.expectedError) > 0:
			expectedErrors := splitExpectedErrors(l.expectedError)
			for _, expectedErr := range expectedErrors {
				require.ErrorContains(t, err, expectedErr)
			}
			return
		default:
			require.NoError(t, err)
		}
	}
}
