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

package controllermutation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
)

type listStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer     *azcosmos.ContainerClient
	expectedControllers []*api.Controller
}

func newListStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*listStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedControllers := []*api.Controller{}
	testContent := api.Must(fs.ReadDir(stepDir, "."))
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" {
			continue
		}

		content, err := fs.ReadFile(stepDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		var controller api.Controller
		if err := json.Unmarshal(content, &controller); err != nil {
			return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
		}
		expectedControllers = append(expectedControllers, &controller)
	}

	return &listStep{
		stepID:              stepID,
		key:                 key,
		cosmosContainer:     cosmosContainer,
		expectedControllers: expectedControllers,
	}, nil
}

var _ controllerMutationStep = &listStep{}

func (l *listStep) StepID() stepID {
	return l.stepID
}

func (l *listStep) RunTest(ctx context.Context, t *testing.T) {
	controllerCRUDClient := controllerCRUDFromKey(t, l.cosmosContainer, l.key)
	actualControllersIterator, err := controllerCRUDClient.List(ctx, nil)
	require.NoError(t, err)

	actualControllers := []*api.Controller{}
	for _, actual := range actualControllersIterator.Items(ctx) {
		actualControllers = append(actualControllers, actual)
	}
	require.NoError(t, actualControllersIterator.GetError())

	if len(l.expectedControllers) != len(actualControllers) {
		t.Logf("actual:\n%v", stringifyControllers(actualControllers))
	}

	require.Equal(t, len(l.expectedControllers), len(actualControllers), "unexpected number of controllers")
	// all the expected must be present
	for _, expected := range l.expectedControllers {
		found := false
		for _, actual := range actualControllers {
			if controllersEqual(expected, actual) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("actual:\n%v", stringifyControllers(actualControllers))
		}
		require.True(t, found, "expected controller not found: %v", expected.ControllerName)
	}

	// all the actual must be expected
	for _, actual := range actualControllers {
		found := false
		for _, expected := range l.expectedControllers {
			if controllersEqual(expected, actual) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("expected:\n%v", stringifyControllers(l.expectedControllers))
		}
		require.True(t, found, "actual controller not found: %v", actual.ControllerName)
	}
}
