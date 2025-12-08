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

package controllermutation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/integrationutils"
)

type loadStep struct {
	stepID stepID

	cosmosContainer *azcosmos.ContainerClient
	content         []byte
}

func newLoadStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, content []byte) *loadStep {
	return &loadStep{
		stepID:          stepID,
		cosmosContainer: cosmosContainer,
		content:         content,
	}
}

var _ controllerMutationStep = &loadStep{}

func (l *loadStep) StepID() stepID {
	return l.stepID
}

func (l *loadStep) RunTest(ctx context.Context, t *testing.T) {
	err := integrationutils.CreateInitialCosmosContent(ctx, l.cosmosContainer, l.content)
	require.NoError(t, err, "failed to load cosmos content")
}
