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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type createStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer *azcosmos.ContainerClient
	controller      *api.Controller
}

func newCreateStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*createStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	content, err := fs.ReadFile(stepDir, "instance.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected.json: %w", err)
	}
	var controller api.Controller
	if err := json.Unmarshal(content, &controller); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
	}

	return &createStep{
		stepID:          stepID,
		key:             key,
		cosmosContainer: cosmosContainer,
		controller:      &controller,
	}, nil
}

var _ controllerMutationStep = &createStep{}

func (l *createStep) StepID() stepID {
	return l.stepID
}

func (l *createStep) RunTest(ctx context.Context, t *testing.T) {
	controllerCRUDClient := controllerCRUDFromKey(t, l.cosmosContainer, l.key)

	// find the existing to set the UID for an create to replace instead of creating a new record.
	existing, err := controllerCRUDClient.Get(ctx, l.controller.ControllerName)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		require.NoError(t, err)
	}
	if existing != nil {
		l.controller.CosmosUID = existing.CosmosUID
	}

	_, err = controllerCRUDClient.Create(ctx, l.controller, nil)
	require.NoError(t, err, "failed to create controller")
}
