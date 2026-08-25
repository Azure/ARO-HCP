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
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type OperationState struct {
	// Source is a name that identifies the source of the operation state.
	Source            string                    `json:"source"`
	ProvisioningState coreapi.ProvisioningState `json:"provisioningState"`
	Message           string                    `json:"message"`
}

// WithSource sets the source of the operation state.
func (s *OperationState) WithSource(source string) *OperationState {
	s.Source = source
	return s
}

// NewOperationState creates a new operation state with the given provisioning state and message, without a source.
func NewOperationState(provisioningState coreapi.ProvisioningState, message string) *OperationState {
	return &OperationState{
		ProvisioningState: provisioningState,
		Message:           message,
	}
}

// provisioningStatePriority is a logical merge order that decides what the most important state to return is.
// For instance, if one check is succeeded, one is failed, and one is accepted, then failed is the most
// reasonable state for the operation.
var provisioningStatePriority = map[coreapi.ProvisioningState]int{
	"":                                      -1, // causes an error
	coreapi.ProvisioningStateFailed:         0,
	coreapi.ProvisioningStateCanceled:       10,
	coreapi.ProvisioningStateDeleting:       20,
	coreapi.ProvisioningStateProvisioning:   30,
	coreapi.ProvisioningStateAwaitingSecret: 35,
	coreapi.ProvisioningStateUpdating:       40,
	coreapi.ProvisioningStateAccepted:       50,
	coreapi.ProvisioningStateSucceeded:      100,
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

// DeadlineExceededMessage returns deadlineSentence, appending remainingChecks
// when it is non-empty.
func DeadlineExceededMessage(deadlineSentence, remainingChecks string) string {
	if remainingChecks == "" {
		return deadlineSentence
	}
	return deadlineSentence + "; " + remainingChecks
}

// PickWorstOperationState expects states pre-sorted and returns the worst state with merged messages.
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
		currentSource := "<no_source>"
		if s.Source != "" {
			currentSource = s.Source
		}
		currentMessage := "<no_message>"
		if s.Message != "" {
			currentMessage = s.Message
		}
		messageParts = append(messageParts, fmt.Sprintf("[%s] %s", currentSource, currentMessage))
	}
	return NewOperationState(worstProvisioningState, strings.Join(messageParts, "; ")), nil
}
