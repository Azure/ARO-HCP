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

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosCompare struct {
	stepID StepID

	cosmosContainer *azcosmos.ContainerClient
	expectedContent []*database.TypedDocument
}

func NewCosmosCompareStep(stepID StepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*cosmosCompare, error) {
	expectedContent, err := readResourcesInDir[database.TypedDocument](stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &cosmosCompare{
		stepID:          stepID,
		cosmosContainer: cosmosContainer,
		expectedContent: expectedContent,
	}, nil
}

var _ IntegrationTestStep = &cosmosCompare{}

func (l *cosmosCompare) StepID() StepID {
	return l.stepID
}

func (l *cosmosCompare) RunTest(ctx context.Context, t *testing.T) {
	// Query all documents in the container
	querySQL := "SELECT * FROM c"
	queryOptions := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{},
	}

	queryPager := l.cosmosContainer.NewQueryItemsPager(querySQL, azcosmos.PartitionKey{}, queryOptions)

	allActual := []*database.TypedDocument{}
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		require.NoError(t, err)

		for _, item := range queryResponse.Items {
			// Parse the document to get its ID for filename
			curr := &database.TypedDocument{}
			err = json.Unmarshal(item, curr)
			require.NoError(t, err)
			allActual = append(allActual, curr)
		}
	}

	typedDocumentSpecializer := UntypedCRUDSpecializer{}
	for _, currExpected := range l.expectedContent {
		found := false
		currDiffs := []string{}
		for _, currActual := range allActual {
			if typedDocumentSpecializer.InstanceEquals(currExpected, currActual) {
				found = true
				break
			}
			currDiffs = append(currDiffs, cmp.Diff(stringifyResource(currExpected), stringifyResource(currActual)))
		}
		if !found {
			t.Log(stringifyResource(allActual))
			for _, diff := range currDiffs {
				t.Log(diff)
			}
			t.Errorf("did not find: %v", currExpected)
		}
	}
}
