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

package integrationutils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ReadGenericMutationTest(testDir fs.FS) (*GenericMutationTest, error) {
	createJSON, err := fs.ReadFile(testDir, "create.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read create.json: %w", err)
	}

	updateJSON, err := fs.ReadFile(testDir, "update.json")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read update.json: %w", err)
	}

	patchJSON, err := fs.ReadFile(testDir, "patch.json")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read patch.json: %w", err)
	}

	expectedErrors := []expectedFieldError{}
	expectedJSON, err := fs.ReadFile(testDir, "expected.json")
	switch {
	case os.IsNotExist(err):
		expectedErrors, err = readExpectedErrors(testDir)
		if err != nil {
			return nil, err
		}

	case err != nil:
		return nil, fmt.Errorf("failed to read expected.json: %w", err)
	}

	var initialCosmosState fs.FS
	if _, err := fs.ReadDir(testDir, "cosmos-state"); err == nil {
		if cosmosState, err := fs.Sub(testDir, "cosmos-state"); err == nil {
			initialCosmosState = cosmosState
		}
	}

	return &GenericMutationTest{
		initialCosmosState: initialCosmosState,
		CreateJSON:         createJSON,
		UpdateJSON:         updateJSON,
		PatchJSON:          patchJSON,
		expectedJSON:       expectedJSON,
		expectedErrors:     expectedErrors,
	}, nil
}

type GenericMutationTest struct {
	initialCosmosState fs.FS
	CreateJSON         []byte
	UpdateJSON         []byte
	PatchJSON          []byte
	expectedJSON       []byte
	expectedErrors     []expectedFieldError
}

func (h *GenericMutationTest) Initialize(ctx context.Context, testInfo *IntegrationTestInfo) error {
	if h.initialCosmosState != nil {
		err := LoadAllContent(ctx, testInfo, h.initialCosmosState)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *GenericMutationTest) IsUpdateTest() bool {
	return len(h.UpdateJSON) > 0
}

func (h *GenericMutationTest) IsPatchTest() bool {
	return len(h.PatchJSON) > 0
}

func (h *GenericMutationTest) ExpectsResult() bool {
	return len(h.expectedJSON) > 0
}

func (h *GenericMutationTest) VerifyActualError(t *testing.T, actualErr error) {
	if len(h.expectedErrors) == 0 {
		require.NoError(t, actualErr)

		return
	}

	require.Error(t, actualErr)

	azureErr, ok := actualErr.(*azcore.ResponseError)
	if !ok {
		t.Fatal(actualErr)
	}

	actualErrors := &arm.CloudError{}
	body, err := io.ReadAll(azureErr.RawResponse.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, actualErrors))
	if len(actualErrors.Details) == 0 { // if we have details, then simulate one so the checking code works easily
		actualErrors.Details = []arm.CloudErrorBody{
			{
				Code:    actualErrors.Code,
				Message: actualErrors.Message,
				Target:  actualErrors.Target,
			},
		}
	}

	for _, actualError := range actualErrors.Details {
		found := false
		for _, expectedErr := range h.expectedErrors {
			if err := expectedErr.matches(actualError); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected error: %s: %s: %s", actualError.Code, actualError.Target, actualError.Message)
		}
	}

	for _, expectedErr := range h.expectedErrors {
		found := false
		for _, actualError := range actualErrors.Details {
			if err := expectedErr.matches(actualError); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected error: %#v", expectedErr)
		}
	}

	if t.Failed() {
		t.Logf("Actual errors: %v", actualErrors)
	}
}

func (h *GenericMutationTest) VerifyActualResult(t *testing.T, actualCreated any) {
	actualJSON, err := json.MarshalIndent(actualCreated, "", "    ")
	require.NoError(t, err)
	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(h.expectedJSON, &expectedMap))

	t.Logf("Actual: %s", actualJSON)
	require.Equal(t, expectedMap, actualMap)
}

func readExpectedErrors(testDir fs.FS) ([]expectedFieldError, error) {
	expectedErrorBytes, err := fs.ReadFile(testDir, "expected-errors.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected-errors.txt: %w", err)
	}

	expectedErrors := []expectedFieldError{}
	expectedErrorLines := strings.Split(string(expectedErrorBytes), "\n")
	for _, currLine := range expectedErrorLines {
		if len(strings.TrimSpace(currLine)) == 0 {
			continue
		}
		tokens := strings.SplitN(currLine, ":", 3)
		currExpected := expectedFieldError{
			code:    strings.TrimSpace(tokens[0]),
			field:   strings.TrimSpace(tokens[1]),
			message: strings.TrimSpace(tokens[2]),
		}
		expectedErrors = append(expectedErrors, currExpected)
	}

	if len(expectedErrors) == 0 {
		return nil, fmt.Errorf("no expected errors found")
	}

	return expectedErrors, nil
}

type expectedFieldError struct {
	code    string
	field   string
	message string
}

func (e expectedFieldError) String() string {
	return fmt.Sprintf("%s: %s: %s", e.code, e.field, e.message)
}

func (e expectedFieldError) matches(actualError arm.CloudErrorBody) error {
	if actualError.Code != e.code {
		return fmt.Errorf("expected code %q, got %q", e.code, actualError.Code)
	}
	if actualError.Target != e.field {
		return fmt.Errorf("expected target %q, got %q", e.field, actualError.Target)
	}
	if !strings.Contains(actualError.Message, e.message) {
		return fmt.Errorf("expected message %q, got %q", e.message, actualError.Message)
	}
	return nil
}
