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

package operations

import (
	"errors"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type OperationState struct {
	ProvisioningState arm.ProvisioningState
	Message           string
}

func NewOperationState(provisioningState arm.ProvisioningState, message string) *OperationState {
	return &OperationState{
		ProvisioningState: provisioningState,
		Message:           message,
	}
}

// provisioningStatePriority is a logical merge order that decides what the most important state to return is.
// For instance, if one check is succeeded, one is failed, and one is accepted, then failed is the most
// reasonable state for the operation.
var provisioningStatePriority = map[arm.ProvisioningState]int{
	"":                                  -1, // causes an error
	arm.ProvisioningStateFailed:         0,
	arm.ProvisioningStateCanceled:       10,
	arm.ProvisioningStateDeleting:       20,
	arm.ProvisioningStateProvisioning:   30,
	arm.ProvisioningStateAwaitingSecret: 35,
	arm.ProvisioningStateUpdating:       40,
	arm.ProvisioningStateAccepted:       50,
	arm.ProvisioningStateSucceeded:      100,
}

func CompareOperationState(lhs, rhs *OperationState) int {
	if lhs == nil && rhs == nil {
		return 0
	}
	if lhs == nil {
		return -1
	}
	if rhs == nil {
		return 1
	}

	if provisioningStatePriority[lhs.ProvisioningState] < provisioningStatePriority[rhs.ProvisioningState] {
		return -1
	}
	if provisioningStatePriority[lhs.ProvisioningState] > provisioningStatePriority[rhs.ProvisioningState] {
		return 1
	}
	return strings.Compare(lhs.Message, rhs.Message)
}

// pickWorstOperationState expects states pre-sorted and returns the worst state with merged messages.
func PickWorstOperationState(states []*OperationState) (*OperationState, error) {
	if len(states) == 0 {
		return nil, errors.New("no operation states")
	}
	worstProvisioningState := states[0].ProvisioningState
	if len(worstProvisioningState) == 0 {
		return nil, errors.New("empty provisioning state")
	}
	var messageParts []string
	for _, s := range states {
		if s.ProvisioningState != worstProvisioningState {
			break
		}
		messageParts = append(messageParts, s.Message)
	}
	return NewOperationState(worstProvisioningState, strings.Join(messageParts, "; ")), nil
}
