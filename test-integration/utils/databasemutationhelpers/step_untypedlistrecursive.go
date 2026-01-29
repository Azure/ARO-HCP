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

	"github.com/Azure/ARO-HCP/internal/database"
)

type UntypedCRUDKey struct {
	CosmosCRUDKey `json:",inline"`

	Descendents []UntypedChild `json:"descendents"`
}

type UntypedChild struct {
	ResourceType string `json:"resourceType"`
	ResourceName string `json:"resourceName"`
}

type untypedListRecursiveStep struct {
	stepID StepID
	key    UntypedCRUDKey

	expectedResources []*database.TypedDocument
}

func newUntypedListRecursiveStep(stepID StepID, stepDir fs.FS) (*untypedListRecursiveStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key UntypedCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, err := readResourcesInDir[database.TypedDocument](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &untypedListRecursiveStep{
		stepID:            stepID,
		key:               key,
		expectedResources: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &untypedListRecursiveStep{}

func (l *untypedListRecursiveStep) StepID() StepID {
	return l.stepID
}

func (l *untypedListRecursiveStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	untypedCRUD, err := stepInput.DBClient.UntypedCRUD(*l.key.ParentResourceID)
	require.NoError(t, err)
	for _, childKey := range l.key.Descendents {
		childResourceType, err := azcorearm.ParseResourceType(childKey.ResourceType)
		require.NoError(t, err)
		untypedCRUD, err = untypedCRUD.Child(childResourceType, childKey.ResourceName)
		require.NoError(t, err)
	}
	actualResourcesIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	require.NoError(t, err)

	actualResources := []*database.TypedDocument{}
	for _, actual := range actualResourcesIterator.Items(ctx) {
		actualResources = append(actualResources, actual)
	}
	require.NoError(t, actualResourcesIterator.GetError())

	if len(l.expectedResources) != len(actualResources) {
		t.Logf("actual:\n%v", stringifyResource(actualResources))
	}

	require.Equal(t, len(l.expectedResources), len(actualResources), "unexpected number of resource")
	// all the expected must be present
	for _, expected := range l.expectedResources {
		found := false
		for _, actual := range actualResources {
			if _, equals := ResourceInstanceEquals(t, expected, actual); equals {
				found = true
				break
			}
		}
		if !found {
			t.Logf("actual:\n%v", stringifyResource(actualResources))
		}
		require.True(t, found, "expected resource not found: %v", ResourceName(expected))
	}

	// all the actual must be expected
	for _, actual := range actualResources {
		found := false
		for _, expected := range l.expectedResources {
			if _, equals := ResourceInstanceEquals(t, expected, actual); equals {
				found = true
				break
			}
		}
		if !found {
			t.Logf("expected:\n%v", stringifyResource(l.expectedResources))
		}
		require.True(t, found, "actual resource not found: %v", ResourceName(actual))
	}
}
