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

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type loadStep struct {
	stepID StepID

	contents [][]byte
}

func NewLoadStep(stepID StepID, stepDir fs.FS) (*loadStep, error) {
	contents, err := readRawBytesInDir(stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &loadStep{
		stepID:   stepID,
		contents: contents,
	}, nil
}

var _ IntegrationTestStep = &loadStep{}

func (l *loadStep) StepID() StepID {
	return l.stepID
}

func (l *loadStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	for _, content := range l.contents {
		err := integrationutils.LoadCosmosContent(ctx, stepInput.CosmosContainer, content)
		require.NoError(t, err, "failed to load cosmos content: %v", string(content))
	}
}
