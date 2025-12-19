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

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type listStep[InternalAPIType any] struct {
	stepID      StepID
	key         CosmosCRUDKey
	specializer ResourceCRUDTestSpecializer[InternalAPIType]

	expectedResources []*InternalAPIType
}

func newListStep[InternalAPIType any](stepID StepID, specializer ResourceCRUDTestSpecializer[InternalAPIType], stepDir fs.FS) (*listStep[InternalAPIType], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedResources, err := readResourcesInDir[InternalAPIType](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &listStep[InternalAPIType]{
		stepID:            stepID,
		key:               key,
		specializer:       specializer,
		expectedResources: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &listStep[any]{}

func (l *listStep[InternalAPIType]) StepID() StepID {
	return l.stepID
}

func (l *listStep[InternalAPIType]) RunTest(ctx context.Context, t *testing.T, cosmosContainer *azcosmos.ContainerClient) {
	controllerCRUDClient := l.specializer.ResourceCRUDFromKey(t, cosmosContainer, l.key)
	actualControllersIterator, err := controllerCRUDClient.List(ctx, nil)
	require.NoError(t, err)

	actualResources := []*InternalAPIType{}
	for _, actual := range actualControllersIterator.Items(ctx) {
		actualResources = append(actualResources, actual)
	}
	require.NoError(t, actualControllersIterator.GetError())

	if len(l.expectedResources) != len(actualResources) {
		t.Logf("actual:\n%v", stringifyResource(actualResources))
	}

	require.Equal(t, len(l.expectedResources), len(actualResources), "unexpected number of resources")
	// all the expected must be present
	for _, expected := range l.expectedResources {
		found := false
		for _, actual := range actualResources {
			if l.specializer.InstanceEquals(expected, actual) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("actual:\n%v", stringifyResource(actualResources))
		}
		require.True(t, found, "expected resource not found: %v", l.specializer.NameFromInstance(expected))
	}

	// all the actual must be expected
	for _, actual := range actualResources {
		found := false
		for _, expected := range l.expectedResources {
			if l.specializer.InstanceEquals(expected, actual) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("expected:\n%v", stringifyResource(l.expectedResources))
		}
		require.True(t, found, "actual resource not found: %v", l.specializer.NameFromInstance(actual))
	}
}
