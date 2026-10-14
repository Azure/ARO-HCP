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
	"errors"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestCopilotSessionErrorCapture(t *testing.T) {
	errorCode := "user_global_rate_limited"
	statusCode := int32(429)
	providerCallID := "provider-call-id"
	serviceRequestID := "service-request-id"
	stack := "provider stack"
	url := "https://example.invalid/rate-limit"
	eligibleForAutoSwitch := true
	capture := &copilotSessionErrorCapture{provider: "github-copilot"}
	capture.record(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType:             CopilotSessionErrorTypeRateLimit,
			ErrorCode:             &errorCode,
			StatusCode:            &statusCode,
			Message:               "request rate limited",
			ProviderCallID:        &providerCallID,
			ServiceRequestID:      &serviceRequestID,
			Stack:                 &stack,
			URL:                   &url,
			EligibleForAutoSwitch: &eligibleForAutoSwitch,
		},
	})

	providerErr := errors.New("CAPIError: 429")
	err := wrapCopilotSessionError(capture, providerErr)

	var sessionErr *CopilotSessionError
	if !errors.As(err, &sessionErr) {
		t.Fatalf("errors.As() = false, want true")
	}
	if sessionErr.Provider != "github-copilot" {
		t.Errorf("Provider = %q, want %q", sessionErr.Provider, "github-copilot")
	}
	if sessionErr.ErrorType != CopilotSessionErrorTypeRateLimit {
		t.Errorf("ErrorType = %q, want %q", sessionErr.ErrorType, CopilotSessionErrorTypeRateLimit)
	}
	if sessionErr.ErrorCode == nil || *sessionErr.ErrorCode != errorCode {
		t.Errorf("ErrorCode = %v, want %q", sessionErr.ErrorCode, errorCode)
	}
	if sessionErr.StatusCode == nil || *sessionErr.StatusCode != statusCode {
		t.Errorf("StatusCode = %v, want %d", sessionErr.StatusCode, statusCode)
	}
	if sessionErr.Message != "request rate limited" {
		t.Errorf("Message = %q, want %q", sessionErr.Message, "request rate limited")
	}
	if sessionErr.ProviderCallID == nil || *sessionErr.ProviderCallID != providerCallID {
		t.Errorf("ProviderCallID = %v, want %q", sessionErr.ProviderCallID, providerCallID)
	}
	if sessionErr.ServiceRequestID == nil || *sessionErr.ServiceRequestID != serviceRequestID {
		t.Errorf("ServiceRequestID = %v, want %q", sessionErr.ServiceRequestID, serviceRequestID)
	}
	if sessionErr.Stack == nil || *sessionErr.Stack != stack {
		t.Errorf("Stack = %v, want %q", sessionErr.Stack, stack)
	}
	if sessionErr.URL == nil || *sessionErr.URL != url {
		t.Errorf("URL = %v, want %q", sessionErr.URL, url)
	}
	if sessionErr.EligibleForAutoSwitch == nil || *sessionErr.EligibleForAutoSwitch != eligibleForAutoSwitch {
		t.Errorf("EligibleForAutoSwitch = %v, want %t", sessionErr.EligibleForAutoSwitch, eligibleForAutoSwitch)
	}
	if !errors.Is(err, providerErr) {
		t.Fatal("errors.Is() = false, want original provider error in the chain")
	}
	if got, want := err.Error(), "copilot session failed: CAPIError: 429"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestCopilotSessionErrorCapturePreservesMissingOptionalFields(t *testing.T) {
	capture := &copilotSessionErrorCapture{provider: "github-copilot"}
	capture.record(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType: CopilotSessionErrorTypeRateLimit,
			Message:   "request rate limited",
		},
	})

	sessionErr := capture.withCause(errors.New("session failed"))
	if sessionErr == nil {
		t.Fatal("withCause() = nil, want CopilotSessionError")
	}
	if sessionErr.ErrorCode != nil ||
		sessionErr.StatusCode != nil ||
		sessionErr.ProviderCallID != nil ||
		sessionErr.ServiceRequestID != nil ||
		sessionErr.Stack != nil ||
		sessionErr.URL != nil ||
		sessionErr.EligibleForAutoSwitch != nil {
		t.Fatalf("optional fields = %#v, want all nil", sessionErr)
	}
}

func TestCopilotSessionErrorCapturePreservesFirstError(t *testing.T) {
	capture := &copilotSessionErrorCapture{provider: "github-copilot"}
	capture.record(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType: CopilotSessionErrorTypeRateLimit,
			Message:   "first error",
		},
	})
	capture.record(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType: "authentication",
			Message:   "second error",
		},
	})

	sessionErr := capture.withCause(errors.New("session failed"))
	if sessionErr == nil {
		t.Fatal("withCause() = nil, want CopilotSessionError")
	}
	if sessionErr.ErrorType != CopilotSessionErrorTypeRateLimit {
		t.Errorf("ErrorType = %q, want %q", sessionErr.ErrorType, CopilotSessionErrorTypeRateLimit)
	}
	if sessionErr.Message != "first error" {
		t.Errorf("Message = %q, want %q", sessionErr.Message, "first error")
	}
}

func TestCopilotSessionErrorCaptureIgnoresUnrelatedEvents(t *testing.T) {
	capture := &copilotSessionErrorCapture{provider: "github-copilot"}
	capture.record(copilot.SessionEvent{Data: &copilot.SessionIdleData{}})

	providerErr := errors.New("session failed")
	err := wrapCopilotSessionError(capture, providerErr)
	var sessionErr *CopilotSessionError
	if errors.As(err, &sessionErr) {
		t.Fatalf("errors.As() = true with %#v, want false", sessionErr)
	}
	if !errors.Is(err, providerErr) {
		t.Fatal("errors.Is() = false, want original provider error in the chain")
	}
	if got, want := err.Error(), "copilot session failed: session failed"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestCopilotSessionErrorWithoutCauseUsesProviderMessage(t *testing.T) {
	err := &CopilotSessionError{Message: "request rate limited"}
	if got, want := err.Error(), "request rate limited"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
