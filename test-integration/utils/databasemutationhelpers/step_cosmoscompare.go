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
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosCompare struct {
	stepID StepID

	expectedContent []*database.TypedDocument
}

func NewCosmosCompareStep(stepID StepID, stepDir fs.FS) (*cosmosCompare, error) {
	expectedContent, err := readResourcesInDir[database.TypedDocument](stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &cosmosCompare{
		stepID:          stepID,
		expectedContent: expectedContent,
	}, nil
}

var _ IntegrationTestStep = &cosmosCompare{}

func (l *cosmosCompare) StepID() StepID {
	return l.stepID
}

func (l *cosmosCompare) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	// Use the DocumentLister interface (works with both Cosmos and mock)
	allActual, err := stepInput.DocumentLister.ListAllDocuments(ctx)
	require.NoError(t, err)

	// Index actuals by ResourceID so each expected document matches at most one
	// actual. Diffs are only computed for the resourceID-matched pair, which
	// keeps failures readable instead of dumping an N*M diff of every actual.
	actualByResourceID := map[string]*database.TypedDocument{}
	for _, currActual := range allActual {
		if currActual.ResourceID == nil {
			continue
		}
		actualByResourceID[strings.ToLower(currActual.ResourceID.String())] = currActual
	}

	for _, currExpected := range l.expectedContent {
		if currExpected.ResourceID == nil {
			t.Errorf("expected document has no resourceID; cannot match:\n%v", stringifyResource(currExpected))
			continue
		}
		currActual, ok := actualByResourceID[strings.ToLower(currExpected.ResourceID.String())]
		if !ok {
			t.Errorf("did not find document with resourceID %s:\n%v", currExpected.ResourceID, stringifyResource(currExpected))
			continue
		}
		diff, equals := ResourceInstanceEquals(t, currExpected, currActual)
		if !equals {
			t.Log(diff)
			t.Errorf("document with resourceID %s did not match expected", currExpected.ResourceID)
		}
	}
}
