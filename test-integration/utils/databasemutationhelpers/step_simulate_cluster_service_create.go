// Copyright 2026 Microsoft Corporation
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

	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type simulateClusterServiceCreateStep struct {
	stepID StepID
	key    ResourceKey
}

func newSimulateClusterServiceCreateStep(stepID StepID, stepDir fs.FS) (*simulateClusterServiceCreateStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read 00-key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal 00-key.json: %w", err)
	}
	if key.ResourceID == "" {
		return nil, fmt.Errorf("00-key.json: resourceId is required")
	}

	return &simulateClusterServiceCreateStep{
		stepID: stepID,
		key:    key,
	}, nil
}

var _ IntegrationTestStep = &simulateClusterServiceCreateStep{}

func (l *simulateClusterServiceCreateStep) StepID() StepID {
	return l.stepID
}

func (l *simulateClusterServiceCreateStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	t.Helper()
	if stepInput.ClusterServiceMockInfo == nil {
		t.Fatal("ClusterServiceMockInfo must not be nil in simulateClusterServiceCreateStep, probably using from the wrong kind of test")
	}

	err := integrationutils.SimulateBackendClusterServiceCreate(
		ctx,
		stepInput.ResourcesDBClient,
		stepInput.ClusterServiceMockInfo,
		l.key.ResourceID,
	)
	require.NoError(t, err)
}
