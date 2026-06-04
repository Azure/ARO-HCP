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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type listActiveOperationsStep struct {
	stepID StepID
	key    CosmosCRUDKey

	expectedOperations []*api.Operation
	expectedFilenames  []string
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

	expectedResources, expectedFilenames, err := readResourcesAndFilenamesInDir[api.Operation](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &listActiveOperationsStep{
		stepID:             stepID,
		key:                key,
		expectedOperations: expectedResources,
		expectedFilenames:  expectedFilenames,
	}, nil
}

var _ IntegrationTestStep = &listActiveOperationsStep{}

// operationKey identifies an Operation by (externalId, request, status) since
// the operation's own resourceID is a UUID re-generated on every run and so is
// stripped by the comparator. listActiveOperations filters to non-terminal
// status server-side; status is still part of the key so a single (resource,
// verb) pair can legitimately appear with more than one non-terminal state
// without collapsing them onto the same fixture.
func operationKey(v any) string {
	op, ok := v.(*api.Operation)
	if !ok {
		return ""
	}
	if op.ExternalID == nil {
		return ""
	}
	return op.ExternalID.String() + "|" + string(op.Request) + "|" + string(op.Status)
}

func (l *listActiveOperationsStep) StepID() StepID {
	return l.stepID
}

func (l *listActiveOperationsStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[api.Operation](t, stepInput.ResourcesDBClient, l.key.ParentResourceID, l.key.ResourceType.ResourceType)

	var operationsCRUD = any(resourceCRUDClient).(database.OperationCRUD)
	actualOperationsIterator := operationsCRUD.ListActiveOperations(nil)

	actualOperations := []*api.Operation{}
	for _, actual := range actualOperationsIterator.Items(ctx) {
		actualOperations = append(actualOperations, actual)
	}
	require.NoError(t, actualOperationsIterator.GetError())

	verifyOrUpdateList(t, l.stepID, toAnySlice(l.expectedOperations), l.expectedFilenames, toAnySlice(actualOperations), operationKey)
}
