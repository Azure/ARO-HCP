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
)

type loadClusterServiceStep struct {
	stepID StepID

	clusterServiceContent fs.FS
}

func NewLoadClusterServiceStep(stepID StepID, clusterServiceContent fs.FS) (*loadClusterServiceStep, error) {
	return &loadClusterServiceStep{
		stepID:                stepID,
		clusterServiceContent: clusterServiceContent,
	}, nil
}

var _ IntegrationTestStep = &loadClusterServiceStep{}

func (l *loadClusterServiceStep) StepID() StepID {
	return l.stepID
}

func (l *loadClusterServiceStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	if stepInput.ClusterServiceMockInfo == nil {
		t.Fatal("ClusterServiceMockInfo must not be nil in loadClusterServiceStep, probably using from the wrong kind of test")
	}
	require.NoError(t, stepInput.ClusterServiceMockInfo.AddContent(t, l.clusterServiceContent))
}
