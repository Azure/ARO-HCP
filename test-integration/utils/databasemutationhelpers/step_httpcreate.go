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

// extractJSONBody strips the TrackError prefix and "HTTP NNN: "
// prefix from error strings, returning the JSON body portion.
// TrackError wraps errors as "(wrapped at file:line) HTTP NNN: {...}".
func extractJSONBody(errStr string) string {
	httpIdx := strings.Index(errStr, "HTTP ")
	if httpIdx >= 0 {
		remainder := errStr[httpIdx:]
		if colonIdx := strings.Index(remainder, ": "); colonIdx >= 0 {
			return remainder[colonIdx+2:]
		}
	}
	return errStr
}

// extractExpectedErrors splits expected-error.txt content into
// individual error objects using json.Decoder streaming.
// All fixtures should be valid JSON objects (normalized as part
// of this work item). Falls back to whole string for any
// remaining non-JSON fixtures.
func extractExpectedErrors(expectedError string) []string {
	expectedError = strings.TrimSpace(expectedError)
	dec := json.NewDecoder(strings.NewReader(expectedError))
	var results []string
	for dec.More() {
		var obj json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			return []string{expectedError}
		}
		results = append(results, string(obj))
	}
	if len(results) == 0 {
		return []string{expectedError}
	}
	return results
}

// errorContainsNormalized checks if the JSON body of actualErr
// contains the expected substring after whitespace normalization.
// Both sides are normalized to single-space separation, making
// comparison insensitive to indentation/formatting differences.
func errorContainsNormalized(t *testing.T, actualErr error, expectedSubstring string) {
	t.Helper()
	require.Error(t, actualErr)
	actualBody := strings.Join(strings.Fields(extractJSONBody(actualErr.Error())), " ")
	expectedNorm := strings.Join(strings.Fields(expectedSubstring), " ")
	require.Contains(t, actualBody, expectedNorm)
}

type httpCreateStep struct {
	stepID StepID
	key    ResourceKey

	resources     [][]byte
	expectedError string
}

func newHTTPCreateStep(stepID StepID, stepDir fs.FS) (*httpCreateStep, error) {
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

	return &httpCreateStep{
		stepID:        stepID,
		key:           key,
		resources:     resources,
		expectedError: expectedError,
	}, nil
}

var _ IntegrationTestStep = &httpCreateStep{}

func (l *httpCreateStep) StepID() StepID {
	return l.stepID
}

func (l *httpCreateStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := stepInput.HTTPTestAccessor(l.key)

	for _, resource := range l.resources {
		err := accessor.CreateOrUpdate(ctx, l.key.ResourceID, resource)

		switch {
		case len(l.expectedError) > 0:
			expectedErrors := extractExpectedErrors(l.expectedError)
			for _, expectedErr := range expectedErrors {
				errorContainsNormalized(t, err, expectedErr)
			}
			return
		default:
			require.NoError(t, err)
		}
	}
}
