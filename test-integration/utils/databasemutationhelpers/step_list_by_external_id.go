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

type listByExternalIDKey struct {
	CosmosCRUDKey
	ExternalID    string `json:"externalId"`
	IncludeNested bool   `json:"includeNested"`
}

type listByExternalIDStep struct {
	stepID StepID
	key    listByExternalIDKey

	expectedOperations []*api.Operation
}

func newListByExternalIDStep(stepID StepID, stepDir fs.FS) (*listByExternalIDStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key listByExternalIDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, err := readResourcesInDir[api.Operation](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &listByExternalIDStep{
		stepID:             stepID,
		key:                key,
		expectedOperations: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &listByExternalIDStep{}

func (l *listByExternalIDStep) StepID() StepID {
	return l.stepID
}

func (l *listByExternalIDStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[api.Operation](t, stepInput.ResourcesDBClient, l.key.ParentResourceID, l.key.ResourceType.ResourceType)

	operationsCRUD, ok := any(resourceCRUDClient).(database.OperationCRUD)
	require.True(t, ok, "resource CRUD does not implement database.OperationCRUD")

	externalID, err := azcorearm.ParseResourceID(l.key.ExternalID)
	require.NoError(t, err, "failed to parse externalId from key")

	actualIterator := operationsCRUD.ListByExternalID(externalID, l.key.IncludeNested)

	actual := []*api.Operation{}
	for _, item := range actualIterator.Items(ctx) {
		actual = append(actual, item)
	}
	require.NoError(t, actualIterator.GetError())

	if len(l.expectedOperations) != len(actual) {
		t.Logf("actual:\n%v", stringifyResource(actual))
	}

	require.Equal(t, len(l.expectedOperations), len(actual), "unexpected number of resources")

	for _, expected := range l.expectedOperations {
		found := false
		for _, a := range actual {
			diff, equals := ResourceInstanceEquals(t, expected, a)
			if equals {
				found = true
				break
			}
			t.Log(diff)
		}
		if !found {
			t.Logf("actual:\n%v", stringifyResource(actual))
		}
		require.True(t, found, "expected resource not found: %v", ResourceName(expected))
	}

	for _, a := range actual {
		found := false
		for _, expected := range l.expectedOperations {
			diff, equals := ResourceInstanceEquals(t, expected, a)
			if equals {
				found = true
				break
			}
			t.Log(diff)
		}
		if !found {
			t.Logf("expected:\n%v", stringifyResource(l.expectedOperations))
		}
		require.True(t, found, "actual resource not found: %v", ResourceName(a))
	}
}
