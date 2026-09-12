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

package v20261001preview

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001preview/generated"
)

func TestNewActiveVersions(t *testing.T) {
	tests := []struct {
		name     string
		input    []coreapi.HCPClusterActiveVersion
		expected []*generated.ClusterActiveVersion
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty input returns nil",
			input:    []coreapi.HCPClusterActiveVersion{},
			expected: nil,
		},
		{
			name: "single version is passed through",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.19"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.19")},
			},
		},
		{
			name: "multiple versions are passed through",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.20"},
				{Version: "4.19"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.20")},
				{Version: ptr.To("4.19")},
			},
		},
		{
			name: "empty version entry is skipped",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.19"},
				{Version: ""},
				{Version: "4.20"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.19")},
				{Version: ptr.To("4.20")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newActiveVersions(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewClusterResourceStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    *coreapi.HCPOpenShiftClusterStatus
		expected *generated.ClusterResourceStatus
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty status returns nil",
			input:    &coreapi.HCPOpenShiftClusterStatus{},
			expected: nil,
		},
		{
			name: "status with active versions only",
			input: &coreapi.HCPOpenShiftClusterStatus{
				ActiveVersions: []coreapi.HCPClusterActiveVersion{
					{Version: "4.20"},
				},
			},
			expected: &generated.ClusterResourceStatus{
				Conditions: nil,
				ActiveVersions: []*generated.ClusterActiveVersion{
					{Version: ptr.To("4.20")},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newClusterResourceStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
