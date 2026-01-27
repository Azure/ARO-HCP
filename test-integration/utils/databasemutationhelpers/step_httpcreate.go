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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type httpCreateStep struct {
	stepID StepID
	key    FrontendResourceKey

	resources     [][]byte
	expectedError string
}

func newHTTPCreateStep(stepID StepID, stepDir fs.FS) (*httpCreateStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key FrontendResourceKey
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
	subscriptionID := api.Must(azcorearm.ParseResourceID(l.key.ResourceID)).SubscriptionID
	accessor := newFrontendHTTPTestAccessor(stepInput.FrontendURL, stepInput.FrontendClient(subscriptionID))

	for _, resource := range l.resources {
		err := accessor.CreateOrUpdate(ctx, l.key.ResourceID, resource)

		switch {
		case len(l.expectedError) > 0:
			// Split expected error by object boundaries to check each error individually
			// This handles multi-error responses where the details array has commas between objects
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

// splitExpectedErrors splits expected error content into individual error objects.
// This handles expected-error.txt files that contain multiple JSON error objects
// which need to be checked individually against the actual error response.
func splitExpectedErrors(expectedError string) []string {
	// Split on the pattern "}\n      {" which separates error objects in expected-error.txt
	// The actual response has "},\n      {" but we check each object as a substring
	parts := strings.Split(expectedError, "}\n      {")
	if len(parts) == 1 {
		// No split found, return the original string
		return []string{expectedError}
	}

	result := make([]string, len(parts))
	for i, part := range parts {
		if i == 0 {
			// First part needs closing brace
			result[i] = part + "}"
		} else if i == len(parts)-1 {
			// Last part needs opening brace
			result[i] = "      {" + part
		} else {
			// Middle parts need both
			result[i] = "      {" + part + "}"
		}
	}
	return result
}
