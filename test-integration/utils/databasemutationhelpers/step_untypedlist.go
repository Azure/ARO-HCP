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
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
)

type untypedListStep struct {
	stepID      StepID
	key         UntypedCRUDKey
	specializer ResourceCRUDTestSpecializer[database.TypedDocument]

	expectedResources []*database.TypedDocument
}

func newUntypedListStep(stepID StepID, stepDir fs.FS) (*untypedListStep, error) {
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

	return &untypedListStep{
		stepID:            stepID,
		key:               key,
		specializer:       UntypedCRUDSpecializer{},
		expectedResources: expectedResources,
	}, nil
}

var _ IntegrationTestStep = &untypedListStep{}

func (l *untypedListStep) StepID() StepID {
	return l.stepID
}

func (l *untypedListStep) RunTest(ctx context.Context, t *testing.T, cosmosContainer *azcosmos.ContainerClient) {
	parentResourceID, err := azcorearm.ParseResourceID(l.key.ParentResourceID)
	require.NoError(t, err)

	untypedCRUD := database.NewUntypedCRUD(cosmosContainer, *parentResourceID)
	for _, childKey := range l.key.Descendents {
		childResourceType, err := azcorearm.ParseResourceType(childKey.ResourceType)
		require.NoError(t, err)
		untypedCRUD, err = untypedCRUD.Child(childResourceType, childKey.ResourceName)
		require.NoError(t, err)
	}
	actualResourcesIterator, err := untypedCRUD.List(ctx, nil)
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
