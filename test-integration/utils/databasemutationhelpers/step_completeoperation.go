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
)

type completeOperationStep struct {
	stepID StepID
	key    ResourceKey
}

func newCompleteOperationStep(stepID StepID, stepDir fs.FS) (*completeOperationStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	return &completeOperationStep{
		stepID: stepID,
		key:    key,
	}, nil
}

var _ IntegrationTestStep = &completeOperationStep{}

func (l *completeOperationStep) StepID() StepID {
	return l.stepID
}

func (l *completeOperationStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := newOperationAccessor(stepInput.DBClient)
	err := accessor.CompleteOperation(ctx, l.key.ResourceID)
	require.NoError(t, err)
}
