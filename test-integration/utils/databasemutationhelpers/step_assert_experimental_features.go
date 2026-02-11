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
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
)

type assertExperimentalFeatures struct {
	stepID          StepID
	expectedOptions expectedExperimentalFeaturesJSON
}

type expectedExperimentalFeaturesJSON struct {
	SingleReplica bool `json:"singleReplica"`
	SizeOverride  bool `json:"sizeOverride"`
}

func newAssertExperimentalFeaturesStep(stepID StepID, stepDir fs.FS) (*assertExperimentalFeatures, error) {
	content, err := fs.ReadFile(stepDir, "expected-options.json")
	if err != nil {
		return nil, err
	}
	var expected expectedExperimentalFeaturesJSON
	if err := json.Unmarshal(content, &expected); err != nil {
		return nil, err
	}
	return &assertExperimentalFeatures{
		stepID:          stepID,
		expectedOptions: expected,
	}, nil
}

var _ IntegrationTestStep = &assertExperimentalFeatures{}

func (s *assertExperimentalFeatures) StepID() StepID {
	return s.stepID
}

func (s *assertExperimentalFeatures) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	require.NotNil(t, stepInput.ClusterServiceMockInfo, "assertExperimentalFeatures requires a ClusterServiceMock")
	require.NotEmpty(t, stepInput.ClusterServiceMockInfo.ExperimentalFeaturesByID, "no ExperimentalFeatures were captured by the mock")

	expected := &api.ExperimentalFeatures{
		SingleReplica: s.expectedOptions.SingleReplica,
		SizeOverride:  s.expectedOptions.SizeOverride,
	}
	// A nil ExperimentalFeatures is equivalent to the zero-value struct (all features disabled).
	zeroValue := api.ExperimentalFeatures{}

	for id, features := range stepInput.ClusterServiceMockInfo.ExperimentalFeaturesByID {
		if features == nil {
			if *expected == zeroValue {
				t.Logf("ExperimentalFeatures for %s is nil, matching expected zero-value: %+v", id, expected)
				return
			}
			continue
		}
		if *features == *expected {
			t.Logf("ExperimentalFeatures for %s matched expected: %+v", id, features)
			return
		}
	}

	t.Errorf("no ExperimentalFeatures entry matched expected %+v, got: %+v",
		expected, stepInput.ClusterServiceMockInfo.ExperimentalFeaturesByID)
}
