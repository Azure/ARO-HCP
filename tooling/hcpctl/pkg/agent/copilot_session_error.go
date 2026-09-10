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

package agent

import (
	"fmt"

	copilot "github.com/github/copilot-sdk/go"
)

const (
	// CopilotSessionErrorTypeRateLimit identifies a Copilot rate-limit response.
	CopilotSessionErrorTypeRateLimit = "rate_limit"
)

// CopilotSessionError preserves structured error details reported by the
// Copilot SDK. Callers can inspect it with errors.As while continuing to handle
// it as a standard error.
type CopilotSessionError struct {
	Provider              string
	ErrorType             string
	ErrorCode             *string
	StatusCode            *int32
	Message               string
	ProviderCallID        *string
	ServiceRequestID      *string
	Stack                 *string
	URL                   *string
	EligibleForAutoSwitch *bool
	Err                   error
}

// Error returns the underlying provider error text when available.
func (e *CopilotSessionError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the original provider error.
func (e *CopilotSessionError) Unwrap() error {
	return e.Err
}

func wrapCopilotSessionErrorData(provider string, data *copilot.SessionErrorData, cause error) error {
	captured := &CopilotSessionError{
		Provider:              provider,
		ErrorType:             data.ErrorType,
		ErrorCode:             clonePointer(data.ErrorCode),
		StatusCode:            clonePointer(data.StatusCode),
		Message:               data.Message,
		ProviderCallID:        clonePointer(data.ProviderCallID),
		ServiceRequestID:      clonePointer(data.ServiceRequestID),
		Stack:                 clonePointer(data.Stack),
		URL:                   clonePointer(data.URL),
		EligibleForAutoSwitch: clonePointer(data.EligibleForAutoSwitch),
		Err:                   cause,
	}
	return fmt.Errorf("copilot session failed: %w", captured)
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
