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
	"slices"
	"testing"

	"github.com/tj/assert"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestNewOperationStateCloudErrorCode(t *testing.T) {
	t.Parallel()

	for _, state := range []coreapi.ProvisioningState{
		coreapi.ProvisioningStateSucceeded,
		coreapi.ProvisioningStateFailed,
		coreapi.ProvisioningStateCanceled,
		coreapi.ProvisioningStateDeleting,
		coreapi.ProvisioningStateProvisioning,
		coreapi.ProvisioningStateAwaitingSecret,
		coreapi.ProvisioningStateUpdating,
		coreapi.ProvisioningStateAccepted,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			wantCode := coreapi.CloudErrorCodeInternalServerError
			if state == coreapi.ProvisioningStateSucceeded {
				wantCode = ""
			}
			got := NewOperationState(state, "diagnostic")
			assert.Equal(t, wantCode, got.CloudErrorCode)
			picked, err := PickWorstOperationState([]*OperationState{got})
			assert.NoError(t, err)
			assert.Equal(t, wantCode, picked.CloudErrorCode)
		})
	}
}

func TestNewFailedOperationStateCloudErrorCode(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"", coreapi.CloudErrorCodeInvalidParameter, coreapi.CloudErrorCodeCapacityHeavyUse} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			wantCode := code
			if wantCode == "" {
				wantCode = coreapi.CloudErrorCodeInternalServerError
			}
			got := NewFailedOperationState(code, "diagnostic", nil)
			assert.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)
			assert.Equal(t, wantCode, got.CloudErrorCode)
			assert.Equal(t, "diagnostic", got.Message)
			assert.Nil(t, got.Error)
		})
	}
}

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
			name: "source with empty message is omitted, not rendered as a placeholder",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "").WithSource("checkA"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "",
		},
		{
			name: "source with empty message is omitted alongside sources with a real message",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateFailed, "").WithSource("checkA"),
				NewOperationState(coreapi.ProvisioningStateFailed, "NotReady: cluster is not ready").WithSource("checkB"),
			},
			wantProv:    coreapi.ProvisioningStateFailed,
			wantMessage: "[checkB] NotReady: cluster is not ready",
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

func TestPickWorstOperationStateErrors(t *testing.T) {
	t.Parallel()

	capacityError := coreapi.CloudErrorBody{
		Code:    coreapi.CloudErrorCodeCapacityHeavyUse,
		Message: "Try again later.",
	}
	otherError := coreapi.CloudErrorBody{
		Code:    coreapi.CloudErrorCodeInternalServerError,
		Message: "An operation failed.",
		Details: []coreapi.CloudErrorBody{{Code: "NestedError", Target: "resource"}},
	}
	tests := []struct {
		name      string
		states    []*OperationState
		wantError *coreapi.CloudErrorBody
	}{
		{
			name:   "no customer errors",
			states: []*OperationState{NewFailedOperationState(coreapi.CloudErrorCodeInternalServerError, "internal diagnostic", nil)},
		},
		{
			name: "single customer error after an unclassified failure",
			states: []*OperationState{
				NewFailedOperationState(coreapi.CloudErrorCodeInternalServerError, "a", nil),
				NewFailedOperationState(capacityError.Code, "b", &capacityError),
			},
			wantError: &capacityError,
		},
		{
			name: "multiple customer errors including an empty diagnostic",
			states: []*OperationState{
				NewFailedOperationState(capacityError.Code, "", &capacityError),
				NewFailedOperationState(otherError.Code, "internal diagnostic", &otherError),
			},
			wantError: &coreapi.CloudErrorBody{
				Code:    coreapi.CloudErrorCodeCapacityHeavyUse,
				Message: "Operation failed due to multiple errors",
				Details: []coreapi.CloudErrorBody{capacityError, otherError},
			},
		},
		{
			name: "code without a customer error outranks a classified failure",
			states: []*OperationState{
				NewFailedOperationState(capacityError.Code, "", &capacityError),
				NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent, "invalid request", nil),
			},
			wantError: &coreapi.CloudErrorBody{
				Code:    coreapi.CloudErrorCodeInvalidRequestContent,
				Message: capacityError.Message,
			},
		},
		{
			name: "errors from other provisioning states are excluded",
			states: []*OperationState{
				NewFailedOperationState(capacityError.Code, "", &capacityError),
				{ProvisioningState: coreapi.ProvisioningStateProvisioning, Error: &otherError},
			},
			wantError: &capacityError,
		},
		{
			name: "nonfailed result has no customer error",
			states: []*OperationState{
				{ProvisioningState: coreapi.ProvisioningStateProvisioning, Error: &otherError},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := PickWorstOperationState(tt.states)
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.wantError, got.Error)
			if got.Error != nil {
				assert.Equal(t, got.CloudErrorCode, got.Error.Code)
			}
			assert.Equal(t, coreapi.CloudErrorCodeCapacityHeavyUse, capacityError.Code)
		})
	}
}

func TestPickWorstCloudErrorCode(t *testing.T) {
	t.Parallel()

	failed := func(code string) *OperationState {
		return NewFailedOperationState(code, "", nil)
	}
	tests := []struct {
		name      string
		states    []*OperationState
		wantCode  string
		wantState coreapi.ProvisioningState
	}{
		{
			name:     "no states defaults to internal server error",
			wantCode: coreapi.CloudErrorCodeInternalServerError,
		},
		{
			name:      "missing code defaults to internal server error",
			states:    []*OperationState{{ProvisioningState: coreapi.ProvisioningStateFailed}},
			wantCode:  coreapi.CloudErrorCodeInternalServerError,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name:      "all internal errors",
			states:    []*OperationState{failed(coreapi.CloudErrorCodeInternalServerError), failed("")},
			wantCode:  coreapi.CloudErrorCodeInternalServerError,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "other codes outrank internal server error",
			states: []*OperationState{
				failed(coreapi.CloudErrorCodeInternalServerError),
				failed(coreapi.CloudErrorCodeCapacityHeavyUse),
			},
			wantCode:  coreapi.CloudErrorCodeCapacityHeavyUse,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "invalid codes outrank other codes",
			states: []*OperationState{
				failed(coreapi.CloudErrorCodeCapacityHeavyUse),
				failed(coreapi.CloudErrorCodeInvalidParameter),
				failed(coreapi.CloudErrorCodeInternalServerError),
			},
			wantCode:  coreapi.CloudErrorCodeInvalidParameter,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "any invalid prefix outranks other codes",
			states: []*OperationState{
				failed("InvalidCustomResource"),
				failed(coreapi.CloudErrorCodeServiceUnavailable),
			},
			wantCode:  "InvalidCustomResource",
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "unrecognized codes have middle priority",
			states: []*OperationState{
				failed("OCM9999"),
				failed(coreapi.CloudErrorCodeInternalServerError),
			},
			wantCode:  "OCM9999",
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "equal priority keeps first code",
			states: []*OperationState{
				failed(coreapi.CloudErrorCodeInvalidResource),
				failed(coreapi.CloudErrorCodeInvalidParameter),
			},
			wantCode:  coreapi.CloudErrorCodeInvalidResource,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "codes from different provisioning states are excluded",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateProvisioning, "").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidParameter),
				failed(coreapi.CloudErrorCodeServiceUnavailable),
				NewOperationState(coreapi.ProvisioningStateSucceeded, "").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidResource),
			},
			wantCode:  coreapi.CloudErrorCodeServiceUnavailable,
			wantState: coreapi.ProvisioningStateFailed,
		},
		{
			name: "pending operations retain codes for deadline failures",
			states: []*OperationState{
				NewOperationState(coreapi.ProvisioningStateProvisioning, "").WithCloudErrorCode(coreapi.CloudErrorCodeCapacityHeavyUse),
				NewOperationState(coreapi.ProvisioningStateSucceeded, "").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidParameter),
			},
			wantCode:  coreapi.CloudErrorCodeCapacityHeavyUse,
			wantState: coreapi.ProvisioningStateProvisioning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantCode, PickWorstCloudErrorCode(tt.states, tt.wantState))
			if len(tt.states) == 0 {
				return
			}
			slices.SortStableFunc(tt.states, CompareOperationState)
			got, err := PickWorstOperationState(tt.states)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			assert.Equal(t, tt.wantCode, got.CloudErrorCode)
		})
	}
}
