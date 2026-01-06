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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type loadCosmosStep struct {
	stepID StepID

	contents [][]byte
}

func NewLoadCosmosStep(stepID StepID, stepDir fs.FS) (*loadCosmosStep, error) {
	contents, err := readRawBytesInDir(stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &loadCosmosStep{
		stepID:   stepID,
		contents: contents,
	}, nil
}

var _ IntegrationTestStep = &loadCosmosStep{}

func (l *loadCosmosStep) StepID() StepID {
	return l.stepID
}

func (l *loadCosmosStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	for _, content := range l.contents {
		err := integrationutils.LoadCosmosContent(ctx, stepInput.CosmosContainer, content)
		require.NoError(t, err, "failed to load cosmos content: %v", string(content))
	}
}
