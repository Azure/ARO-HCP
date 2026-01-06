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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type listActiveOperationsStep struct {
	stepID StepID
	key    CosmosCRUDKey

	expectedOperations []*api.Operation
}

func newListActiveOperationsStep(stepID StepID, stepDir fs.FS) (*listActiveOperationsStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, err := readResourcesInDir[api.Operation](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &listActiveOperationsStep{
		stepID:             stepID,
		key:                key,
		expectedOperations: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &listActiveOperationsStep{}

func (l *listActiveOperationsStep) StepID() StepID {
	return l.stepID
}

func (l *listActiveOperationsStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	parentResourceID, err := azcorearm.ParseResourceID(l.key.ParentResourceID)
	require.NoError(t, err)

	operationsCRUD := database.NewOperationCRUD(stepInput.CosmosContainer, parentResourceID.SubscriptionID)
	actualControllersIterator := operationsCRUD.ListActiveOperations(nil)
	require.NoError(t, err)

	actualControllers := []*database.OperationDocument{}
	for _, actual := range actualControllersIterator.Items(ctx) {
		actualControllers = append(actualControllers, actual)
	}
	require.NoError(t, actualControllersIterator.GetError())

	if len(l.expectedOperations) != len(actualControllers) {
		t.Logf("actual:\n%v", stringifyResource(actualControllers))
	}

	specializer := OperationCRUDSpecializer{}
	require.Equal(t, len(l.expectedOperations), len(actualControllers), "unexpected number of resources")
	// all the expected must be present
	for _, expected := range l.expectedOperations {
		found := false
		for _, actual := range actualControllers {
			diff, equals := ResourceInstanceEquals(t, expected, actual)
			if equals {
				found = true
				break
			}
			t.Log(diff)
		}
		if !found {
			t.Logf("actual:\n%v", stringifyResource(actualControllers))
		}
		require.True(t, found, "expected resource not found: %v", specializer.NameFromInstance(expected))
	}

	// all the actual must be expected
	for _, actual := range actualControllers {
		found := false
		for _, expected := range l.expectedOperations {
			diff, equals := ResourceInstanceEquals(t, expected, actual)
			if equals {
				found = true
				break
			}
			t.Log(diff)
		}
		if !found {
			t.Logf("expected:\n%v", stringifyResource(l.expectedOperations))
		}
		require.True(t, found, "actual resource not found: %v", specializer.NameFromInstance(actual))
	}
}
