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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

type clusterServiceCompare struct {
	stepID          StepID
	expectedContent map[string]any
}

func newClusterServiceCompareStep(stepID StepID, stepDir fs.FS) (*clusterServiceCompare, error) {
	content, err := fs.ReadFile(stepDir, "expected-content.json")
	if err != nil {
		return nil, fmt.Errorf("reading expected-content.json for step %s: %w", stepID, err)
	}
	var expected map[string]any
	if err := json.Unmarshal(content, &expected); err != nil {
		return nil, fmt.Errorf("unmarshaling expected-content.json for step %s: %w", stepID, err)
	}
	return &clusterServiceCompare{
		stepID:          stepID,
		expectedContent: expected,
	}, nil
}

var _ IntegrationTestStep = &clusterServiceCompare{}

func (s *clusterServiceCompare) StepID() StepID {
	return s.stepID
}

func (s *clusterServiceCompare) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	require.NotNil(t, stepInput.ClusterServiceMockInfo, "clusterServiceCompare requires a ClusterServiceMock")

	clusters, err := stepInput.ClusterServiceMockInfo.GetMergedClusters(t.Name())
	require.NoError(t, err)
	require.NotEmpty(t, clusters, "no clusters found in ClusterServiceMock")

	for _, cluster := range clusters {
		if fieldsMatch(s.expectedContent, cluster) {
			return
		}
	}

	// No match found — report diffs for debugging.
	for i, cluster := range clusters {
		diff := cmp.Diff(s.expectedContent, extractFields(s.expectedContent, cluster))
		t.Logf("cluster %d diff:\n%s", i, diff)
	}
	expectedJSON, err := json.MarshalIndent(s.expectedContent, "", "  ")
	require.NoError(t, err, "marshaling expected content for error message")
	t.Errorf("no cluster matched expected content:\n%s", expectedJSON)
}

// fieldsMatch checks that every key in expected exists in actual with an
// equal value. For nested maps, the comparison recurses. An empty expected
// map requires the actual map to also be empty (or absent), so
// "properties": {} asserts that no properties are set.
func fieldsMatch(expected, actual map[string]any) bool {
	for k, ev := range expected {
		av, ok := actual[k]
		if !ok {
			return false
		}
		em, emOK := ev.(map[string]any)
		am, amOK := av.(map[string]any)
		if emOK && amOK {
			// An empty expected map means the actual must also be empty.
			if len(em) == 0 {
				return len(am) == 0
			}
			// Properties must be an exact match — no extra keys allowed.
			if k == "properties" && len(am) != len(em) {
				return false
			}
			if !fieldsMatch(em, am) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(ev, av) {
			return false
		}
	}
	return true
}

// extractFields returns a subset of actual containing only the keys present
// in expected, for readable diff output.
func extractFields(expected, actual map[string]any) map[string]any {
	result := map[string]any{}
	for k, ev := range expected {
		av, ok := actual[k]
		if !ok {
			continue
		}
		em, emOK := ev.(map[string]any)
		am, amOK := av.(map[string]any)
		if emOK && amOK {
			result[k] = extractFields(em, am)
			continue
		}
		result[k] = av
	}
	return result
}
