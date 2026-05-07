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

package operationcontrollers

import (
	"testing"

	"github.com/tj/assert"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestCompareOperationState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lhs      *operationState
		rhs      *operationState
		expected int
	}{
		{
			name:     "both nil",
			lhs:      nil,
			rhs:      nil,
			expected: 0,
		},
		{
			name:     "lhs nil",
			lhs:      nil,
			rhs:      newOperationState(arm.ProvisioningStateSucceeded, ""),
			expected: -1,
		},
		{
			name:     "rhs nil",
			lhs:      newOperationState(arm.ProvisioningStateSucceeded, ""),
			rhs:      nil,
			expected: 1,
		},
		{
			name:     "Succeeded > Provisioning",
			lhs:      newOperationState(arm.ProvisioningStateSucceeded, ""),
			rhs:      newOperationState(arm.ProvisioningStateProvisioning, ""),
			expected: 1,
		},
		{
			name:     "Deleting < Provisioning",
			lhs:      newOperationState(arm.ProvisioningStateDeleting, ""),
			rhs:      newOperationState(arm.ProvisioningStateProvisioning, ""),
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := compareOperationState(tt.lhs, tt.rhs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPickWorstOperationState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		states      []*operationState
		wantErr     string
		wantProv    arm.ProvisioningState
		wantMessage string
	}{
		{
			name:    "empty slice nil",
			states:  nil,
			wantErr: "no operation states",
		},
		{
			name:    "empty slice non-nil",
			states:  []*operationState{},
			wantErr: "no operation states",
		},
		{
			name: "first state has empty provisioning state",
			states: []*operationState{
				newOperationState("", "ignored"),
			},
			wantErr: "empty provisioning state",
		},
		{
			name: "single state",
			states: []*operationState{
				newOperationState(arm.ProvisioningStateFailed, "first failure"),
			},
			wantProv:    arm.ProvisioningStateFailed,
			wantMessage: "first failure",
		},
		{
			name: "merges messages for consecutive same provisioning state",
			states: []*operationState{
				newOperationState(arm.ProvisioningStateFailed, "a"),
				newOperationState(arm.ProvisioningStateFailed, "b"),
				newOperationState(arm.ProvisioningStateFailed, "c"),
			},
			wantProv:    arm.ProvisioningStateFailed,
			wantMessage: "a; b; c",
		},
		{
			name: "stops merging when provisioning state changes",
			states: []*operationState{
				newOperationState(arm.ProvisioningStateFailed, "worst"),
				newOperationState(arm.ProvisioningStateSucceeded, "ignored"),
			},
			wantProv:    arm.ProvisioningStateFailed,
			wantMessage: "worst",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pickWorstOperationState(tt.states)
			if tt.wantErr != "" {
				assert.Nil(t, got)
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.wantProv, got.provisioningState)
			assert.Equal(t, tt.wantMessage, got.message)
		})
	}
}
