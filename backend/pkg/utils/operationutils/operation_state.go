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
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type OperationState struct {
	// Source is a name that identifies the source of the operation state.
	Source            string                    `json:"source"`
	ProvisioningState coreapi.ProvisioningState `json:"provisioningState"`
	Message           string                    `json:"message"`

	// CloudErrorCode defaults to InternalServerError for non-successful states.
	CloudErrorCode string `json:"cloudErrorCode"`

	// Error is the customer-safe error for a failed operation
	Error *coreapi.CloudErrorBody `json:"error,omitempty"`
}

// WithSource sets the source of the operation state.
func (s *OperationState) WithSource(source string) *OperationState {
	s.Source = source
	return s
}

// WithCloudErrorCode sets the error code when a more specific classification is available.
func (s *OperationState) WithCloudErrorCode(code string) *OperationState {
	if code != "" {
		s.CloudErrorCode = code
	}
	return s
}

// NewOperationState creates a new operation state with the given provisioning state and message, without a source.
func NewOperationState(provisioningState coreapi.ProvisioningState, message string) *OperationState {
	code := ""
	if provisioningState != coreapi.ProvisioningStateSucceeded {
		code = coreapi.CloudErrorCodeInternalServerError
	}
	return &OperationState{
		ProvisioningState: provisioningState,
		Message:           message,
		CloudErrorCode:    code,
	}
}

// NewFailedOperationState creates a failed operation state with an explicit code,
// diagnostic message and optional customer-safe error. An empty code defaults to
// InternalServerError.
func NewFailedOperationState(code, message string, operationError *coreapi.CloudErrorBody) *OperationState {
	state := NewOperationState(coreapi.ProvisioningStateFailed, message).WithCloudErrorCode(code)
	state.Error = operationError
	return state
}

// PickWorstCloudErrorCode selects a code from states matching provisioningState.
// Customer caused codes (coreapi.IsCustomerCaused) rank worst, followed by
// Invalid* codes, other codes, then InternalServerError.
// Successful states have no error code. Otherwise, missing codes default to
// InternalServerError; equal priorities keep the first code.
func PickWorstCloudErrorCode(states []*OperationState, provisioningState coreapi.ProvisioningState) string {
	if provisioningState == coreapi.ProvisioningStateSucceeded {
		return ""
	}
	code := coreapi.CloudErrorCodeInternalServerError
	for _, state := range states {
		if state.ProvisioningState != provisioningState || state.CloudErrorCode == "" {
			continue
		}
		if cloudErrorCodePriority(state.CloudErrorCode) < cloudErrorCodePriority(code) {
			code = state.CloudErrorCode
		}
	}
	return code
}

func cloudErrorCodePriority(code string) int {
	switch {
	// Customer caused codes win so the operation is attributed to the customer
	// until they resolve it; system health cannot be judged before then.
	case coreapi.IsCustomerCaused(code):
		return -1
	case strings.HasPrefix(code, "Invalid"):
		return 0
	case code == coreapi.CloudErrorCodeInternalServerError:
		return 2
	default:
		return 1
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

// PersistedOperationError returns the error to persist on a failed operation. It
// prefers the customer safe state.Error. The diagnostic state.Message holds source
// tags and upstream text, so it is only the fallback when no safe error exists, and
// callers must log it. It returns nil for states that have not failed.
func PersistedOperationError(state *OperationState) *coreapi.CloudErrorBody {
	if state == nil || state.ProvisioningState != coreapi.ProvisioningStateFailed {
		return nil
	}
	if state.Error != nil {
		return state.Error
	}
	return &coreapi.CloudErrorBody{Code: state.CloudErrorCode, Message: state.Message}
}

// LogOperationFailure logs the diagnostic detail of a failed state next to the
// customer facing error, so detail withheld from the customer is never lost.
// It does nothing when persisted is nil.
func LogOperationFailure(ctx context.Context, operationKind string, state *OperationState, persisted *coreapi.CloudErrorBody) {
	if state == nil || persisted == nil {
		return
	}
	utils.LoggerFromContext(ctx).Info(operationKind+" operation failed",
		"cloudErrorCode", state.CloudErrorCode,
		"diagnosticMessage", state.Message,
		"customerCode", persisted.Code,
		"customerMessage", persisted.Message)
}

// PickWorstOperationState expects states pre-sorted and returns the worst state with merged messages.
//
// Only sources that report an actual message are included in the merged message: a source tied
// for the worst provisioning state with no message of its own has nothing blocking to report (e.g.
// it is simply still in progress), so it is omitted rather than rendered as a confusing "<no_message>"
// placeholder that reads like an error.
//
// Customer-safe errors from failed states are collected independently of their messages.
// No errors yields nil; one retains its message and details; multiple are wrapped
// in a combined error with the original errors in Details. The resulting error
// uses the worst code from all states with the selected provisioning state.
func PickWorstOperationState(states []*OperationState) (*OperationState, error) {
	if len(states) == 0 {
		return nil, errors.New("no operation states")
	}
	worstProvisioningState := states[0].ProvisioningState
	if len(worstProvisioningState) == 0 {
		return nil, errors.New("empty provisioning state")
	}
	code := PickWorstCloudErrorCode(states, worstProvisioningState)
	// When a customer caused code wins, only customer caused errors are shown to
	// the customer. Other errors stay in the diagnostic message, which is logged.
	customerWins := worstProvisioningState == coreapi.ProvisioningStateFailed && coreapi.IsCustomerCaused(code)
	var messageParts []string
	var operationErrors []coreapi.CloudErrorBody
	for _, s := range states {
		if s.ProvisioningState != worstProvisioningState {
			break
		}
		if s.Error != nil && (!customerWins || coreapi.IsCustomerCaused(s.Error.Code)) {
			operationErrors = append(operationErrors, *s.Error)
		}
		if s.Message == "" {
			continue
		}
		currentSource := "<no_source>"
		if s.Source != "" {
			currentSource = s.Source
		}
		messageParts = append(messageParts, fmt.Sprintf("[%s] %s", currentSource, s.Message))
	}
	message := strings.Join(messageParts, "; ")
	if worstProvisioningState == coreapi.ProvisioningStateFailed {
		operationError := coreapi.NewCloudErrorBodyFromSlice(operationErrors, "Operation failed due to multiple errors")
		if operationError != nil {
			operationError.Code = code
		}
		return NewFailedOperationState(code, message, operationError), nil
	}
	return NewOperationState(worstProvisioningState, message).WithCloudErrorCode(code), nil
}
