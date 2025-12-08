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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
)

type getStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer    *azcosmos.ContainerClient
	expectedController *api.Controller
	expectedError      string
}

func newGetStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*getStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	var expectedController *api.Controller
	testContent := api.Must(fs.ReadDir(stepDir, "."))
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" || dirEntry.Name() == "expected-error.txt" {
			continue
		}
		if !strings.HasSuffix(dirEntry.Name(), ".json") {
			continue
		}
		if expectedController != nil {
			return nil, fmt.Errorf("too many expectedControllers found %s", dirEntry.Name())
		}

		content, err := fs.ReadFile(stepDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		expectedController = &api.Controller{}
		if err := json.Unmarshal(content, expectedController); err != nil {
			return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
		}
	}

	if len(expectedError) == 0 && expectedController == nil {
		return nil, fmt.Errorf("must expect either error and value")
	}

	return &getStep{
		stepID:             stepID,
		key:                key,
		cosmosContainer:    cosmosContainer,
		expectedController: expectedController,
		expectedError:      expectedError,
	}, nil
}

var _ controllerMutationStep = &getStep{}

func (l *getStep) StepID() stepID {
	return l.stepID
}

func (l *getStep) RunTest(ctx context.Context, t *testing.T) {
	controllerCRUDClient := controllerCRUDFromKey(t, l.cosmosContainer, l.key)
	actualController, err := controllerCRUDClient.Get(ctx, l.expectedController.ControllerName)
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

	if !controllersEqual(l.expectedController, actualController) {
		t.Logf("actual:\n%v", stringifyController(actualController))
		// cmpdiff doesn't handle private fields gracefully
		require.Equal(t, l.expectedController, actualController)
		t.Fatal("unexpected")
	}
}
