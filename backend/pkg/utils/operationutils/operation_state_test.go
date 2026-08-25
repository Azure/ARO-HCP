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

package operationutils

import (
	"testing"

	"github.com/tj/assert"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestCompareOperationState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lhs      *OperationState
		rhs      *OperationState
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
			rhs:      NewOperationState(coreapi.ProvisioningStateSucceeded, ""),
			expected: -1,
		},
		{
			name:     "rhs nil",
			lhs:      NewOperationState(coreapi.ProvisioningStateSucceeded, ""),
			rhs:      nil,
			expected: 1,
		},
		{
			name:     "Succeeded > Provisioning",
			lhs:      NewOperationState(coreapi.ProvisioningStateSucceeded, ""),
			rhs:      NewOperationState(coreapi.ProvisioningStateProvisioning, ""),
			expected: 1,
		},
		{
			name:     "Deleting < Provisioning",
			lhs:      NewOperationState(coreapi.ProvisioningStateDeleting, ""),
			rhs:      NewOperationState(coreapi.ProvisioningStateProvisioning, ""),
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := CompareOperationState(tt.lhs, tt.rhs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeadlineExceededMessage(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "cluster creation did not complete before the deadline",
		DeadlineExceededMessage("cluster creation did not complete before the deadline", ""))
	assert.Equal(t,
		"cluster creation did not complete before the deadline; [clusterServiceClusterStatus] cluster service is installing",
		DeadlineExceededMessage(
			"cluster creation did not complete before the deadline",
			"[clusterServiceClusterStatus] cluster service is installing",
		),
	)
}

func TestPickWorstOperationState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		states      []*OperationState
		wantErr     string
		wantProv    coreapi.ProvisioningState
		wantMessage string
	}{
		{
			name:    "empty slice nil",
			states:  nil,
			wantErr: "no operation states",
		},
		{
			name:    "empty slice non-nil",
			states:  []*OperationState{},
			wantErr: "no operation states",
		},
		{
			name: "first state has empty provisioning state",
			states: []*OperationState{
				NewOperationState("", "ignored"),
			},
			wantErr: "empty provisioning state",
		},
		{
			name: "single state without source",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "first failure"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[<no_source>] first failure",
		},
		{
			name: "single state with source",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "NotReady: cluster is not ready").WithSource("hypershiftHostedCluster"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[hypershiftHostedCluster] NotReady: cluster is not ready",
		},
		{
			name: "merges messages for consecutive same provisioning state",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "a"),
				NewOperationState(coreapi.ProvisioningStateFailed, "b"),
				NewOperationState(coreapi.ProvisioningStateFailed, "c"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[<no_source>] a; [<no_source>] b; [<no_source>] c",
		},
		{
			name: "merges messages with sources",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "a").WithSource("checkA"),
				NewOperationState(coreapi.ProvisioningStateFailed, "b").WithSource("checkB"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[checkA] a; [checkB] b",
		},
		{
			name: "stops merging when provisioning state changes",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "worst"),
				NewOperationState(coreapi.ProvisioningStateSucceeded, "ignored"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[<no_source>] worst",
		},
		{
			name: "empty message uses placeholder",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "").WithSource("checkA"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[checkA] <no_message>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := PickWorstOperationState(tt.states)
			if tt.wantErr != "" {
				assert.Nil(t, got)
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.wantProv, got.ProvisioningState)
			assert.Equal(t, tt.wantMessage, got.Message)
		})
	}
}
