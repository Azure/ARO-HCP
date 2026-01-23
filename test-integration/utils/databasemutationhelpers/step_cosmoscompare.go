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
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

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
	var allActual []*database.TypedDocument
	var err error

	if stepInput.DocumentLister != nil {
		// Use the DocumentLister interface (works with both Cosmos and mock)
		allActual, err = stepInput.DocumentLister.ListAllDocuments(ctx)
		require.NoError(t, err)
	} else if stepInput.CosmosContainer != nil {
		// Fallback to direct Cosmos querying
		querySQL := "SELECT * FROM c"
		queryOptions := &azcosmos.QueryOptions{
			QueryParameters: []azcosmos.QueryParameter{},
		}

		queryPager := stepInput.CosmosContainer.NewQueryItemsPager(querySQL, azcosmos.PartitionKey{}, queryOptions)

		for queryPager.More() {
			queryResponse, queryErr := queryPager.NextPage(ctx)
			require.NoError(t, queryErr)

			for _, item := range queryResponse.Items {
				curr := &database.TypedDocument{}
				err = json.Unmarshal(item, curr)
				require.NoError(t, err)
				allActual = append(allActual, curr)
			}
		}
	} else {
		t.Fatal("neither DocumentLister nor CosmosContainer is set")
	}

	for _, currExpected := range l.expectedContent {
		found := false
		currDiffs := []string{}
		for _, currActual := range allActual {
			diff, equals := ResourceInstanceEquals(t, currExpected, currActual)
			if equals {
				found = true
				break
			}
			currDiffs = append(currDiffs, diff)
		}
		if !found {
			t.Log(stringifyResource(allActual))
			for _, diff := range currDiffs {
				t.Log(diff)
			}
			t.Errorf("did not find:\n%v", stringifyResource(currExpected))
		}
	}
}
