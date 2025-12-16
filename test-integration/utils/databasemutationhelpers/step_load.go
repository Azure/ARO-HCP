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
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type loadStep struct {
	stepID StepID

	cosmosContainer *azcosmos.ContainerClient
	contents        [][]byte
}

func NewLoadStep(stepID StepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*loadStep, error) {

	contents := [][]byte{}
	testContent := api.Must(fs.ReadDir(stepDir, "."))
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" { // standard filenames to skip
			continue
		}
		if dirEntry.Name() == "expected-error.txt" { // standard filenames to skip
			continue
		}
		if !strings.HasSuffix(dirEntry.Name(), ".json") { // we can only understand JSON
			continue
		}

		currContent, err := fs.ReadFile(stepDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		contents = append(contents, currContent)
	}

	return &loadStep{
		stepID:          stepID,
		cosmosContainer: cosmosContainer,
		contents:        contents,
	}, nil
}

var _ IntegrationTestStep = &loadStep{}

func (l *loadStep) StepID() StepID {
	return l.stepID
}

func (l *loadStep) RunTest(ctx context.Context, t *testing.T) {
	for _, content := range l.contents {
		err := integrationutils.LoadCosmosContent(ctx, l.cosmosContainer, content)
		require.NoError(t, err, "failed to load cosmos content: %v", string(content))
	}
}
