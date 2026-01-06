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
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
)

type untypedGetStep struct {
	stepID      StepID
	key         UntypedCRUDKey
	specializer ResourceCRUDTestSpecializer[database.TypedDocument]

	expectedResource *database.TypedDocument
	expectedError    string
}

func newUntypedGetStep(stepID StepID, stepDir fs.FS) (*untypedGetStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key UntypedCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	var expectedResource *database.TypedDocument
	expectedResources, err := readResourcesInDir[database.TypedDocument](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}
	switch len(expectedResources) {
	case 0:
	case 1:
		expectedResource = expectedResources[0]
	default:
		return nil, fmt.Errorf("cannot expect more than one resource")
	}

	if len(expectedError) == 0 && expectedResource == nil {
		return nil, fmt.Errorf("must expect either error and value")
	}

	return &untypedGetStep{
		stepID:           stepID,
		key:              key,
		specializer:      UntypedCRUDSpecializer{},
		expectedResource: expectedResource,
		expectedError:    expectedError,
	}, nil
}

var _ IntegrationTestStep = &untypedGetStep{}

func (l *untypedGetStep) StepID() StepID {
	return l.stepID
}

func (l *untypedGetStep) RunTest(ctx context.Context, t *testing.T, cosmosContainer *azcosmos.ContainerClient) {
	parentResourceID, err := azcorearm.ParseResourceID(l.key.ParentResourceID)
	require.NoError(t, err)

	untypedCRUD := database.NewUntypedCRUD(cosmosContainer, *parentResourceID)
	for _, childKey := range l.key.Descendents {
		childResourceType, err := azcorearm.ParseResourceType(childKey.ResourceType)
		require.NoError(t, err)
		untypedCRUD, err = untypedCRUD.Child(childResourceType, childKey.ResourceName)
		require.NoError(t, err)
	}
	actualResource, err := untypedCRUD.Get(ctx, parentResourceID)
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

	if !l.specializer.InstanceEquals(l.expectedResource, actualResource) {
		t.Logf("actual:\n%v", stringifyResource(actualResource))
		// cmpdiff doesn't handle private fields gracefully
		require.Equal(t, l.expectedResource, actualResource)
		t.Fatal("unexpected")
	}
}
