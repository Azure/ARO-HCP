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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

type fakeCopilotSendSession struct {
	mu      sync.Mutex
	handler copilot.SessionEventHandler
	send    func(context.Context, copilot.MessageOptions) (string, error)
}

func (f *fakeCopilotSendSession) Send(ctx context.Context, options copilot.MessageOptions) (string, error) {
	return f.send(ctx, options)
}

func (f *fakeCopilotSendSession) On(handler copilot.SessionEventHandler) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
	return func() {}
}

func (f *fakeCopilotSendSession) emit(event copilot.SessionEvent) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	handler(event)
}

func TestCopilotSendDispatcherHandlesEventsBeforeSendReturns(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	fake.send = func(context.Context, copilot.MessageOptions) (string, error) {
		interactionID := "interaction-1"
		turnID := "turn-1"
		serviceRequestID := "service-request-1"
		requestID := "provider-call-1"
		fake.emit(copilot.SessionEvent{
			ID: "message-1",
			Data: &copilot.UserMessageData{
				InteractionID: &interactionID,
			},
		})
		fake.emit(copilot.SessionEvent{
			Data: &copilot.AssistantTurnStartData{
				InteractionID: &interactionID,
				TurnID:        turnID,
			},
		})
		fake.emit(copilot.SessionEvent{
			Data: &copilot.AssistantMessageData{
				Content:          "answer",
				MessageID:        "assistant-message-1",
				InteractionID:    &interactionID,
				TurnID:           &turnID,
				ServiceRequestID: &serviceRequestID,
				RequestID:        &requestID,
			},
		})
		fake.emit(copilot.SessionEvent{Data: &copilot.SessionIdleData{}})
		return "message-1", nil
	}

	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	event, err := dispatcher.sendAndWait(t.Context(), copilot.MessageOptions{Prompt: "prompt"}, func() {})
	if err != nil {
		t.Fatalf("sendAndWait() error = %v", err)
	}
	data, ok := event.Data.(*copilot.AssistantMessageData)
	if !ok {
		t.Fatalf("event data type = %T, want *copilot.AssistantMessageData", event.Data)
	}
	if data.Content != "answer" {
		t.Errorf("Content = %q, want %q", data.Content, "answer")
	}
}

func TestCopilotSendDispatcherReturnsCorrelatedStructuredError(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	fake.send = func(context.Context, copilot.MessageOptions) (string, error) {
		interactionID := "interaction-1"
		serviceRequestID := "service-request-1"
		providerCallID := "provider-call-1"
		fake.emit(copilot.SessionEvent{
			ID: "message-1",
			Data: &copilot.UserMessageData{
				InteractionID: &interactionID,
			},
		})
		fake.emit(copilot.SessionEvent{
			Data: &copilot.AssistantTurnStartData{
				InteractionID: &interactionID,
				TurnID:        "turn-1",
			},
		})
		fake.emit(copilot.SessionEvent{
			Data: &copilot.ModelCallFailureData{
				ServiceRequestID: &serviceRequestID,
				ProviderCallID:   &providerCallID,
			},
		})
		fake.emit(copilot.SessionEvent{
			Data: &copilot.SessionErrorData{
				ErrorType:        CopilotSessionErrorTypeRateLimit,
				Message:          "request rate limited",
				ServiceRequestID: &serviceRequestID,
				ProviderCallID:   &providerCallID,
			},
		})
		return "message-1", nil
	}

	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	_, err := dispatcher.sendAndWait(t.Context(), copilot.MessageOptions{Prompt: "prompt"}, func() {})
	var sessionErr *CopilotSessionError
	if !errors.As(err, &sessionErr) {
		t.Fatalf("errors.As() = false for %v, want true", err)
	}
	if sessionErr.ServiceRequestID == nil || *sessionErr.ServiceRequestID != "service-request-1" {
		t.Errorf("ServiceRequestID = %v, want %q", sessionErr.ServiceRequestID, "service-request-1")
	}
	if sessionErr.ProviderCallID == nil || *sessionErr.ProviderCallID != "provider-call-1" {
		t.Errorf("ProviderCallID = %v, want %q", sessionErr.ProviderCallID, "provider-call-1")
	}
}

func TestCopilotSendDispatcherSeparatesConcurrentRequests(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	sent := make(chan string, 2)
	var sendMu sync.Mutex
	sendCount := 0
	fake.send = func(context.Context, copilot.MessageOptions) (string, error) {
		sendMu.Lock()
		sendCount++
		id := fmt.Sprintf("message-%d", sendCount)
		interactionID := fmt.Sprintf("interaction-%d", sendCount)
		sendMu.Unlock()
		fake.emit(copilot.SessionEvent{
			ID: id,
			Data: &copilot.UserMessageData{
				InteractionID: &interactionID,
			},
		})
		sent <- id
		return id, nil
	}

	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	type result struct {
		event *copilot.SessionEvent
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			event, err := dispatcher.sendAndWait(t.Context(), copilot.MessageOptions{Prompt: "prompt"}, func() {})
			results <- result{event: event, err: err}
		}()
	}
	<-sent
	<-sent

	interactionID1 := "interaction-1"
	serviceRequestID1 := "service-request-1"
	fake.emit(copilot.SessionEvent{
		Data: &copilot.AssistantTurnStartData{
			InteractionID: &interactionID1,
			TurnID:        "turn-1",
		},
	})
	fake.emit(copilot.SessionEvent{
		Data: &copilot.ModelCallFailureData{
			ServiceRequestID: &serviceRequestID1,
		},
	})
	fake.emit(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType:        CopilotSessionErrorTypeRateLimit,
			Message:          "first request failed",
			ServiceRequestID: &serviceRequestID1,
		},
	})

	interactionID2 := "interaction-2"
	turnID2 := "turn-2"
	fake.emit(copilot.SessionEvent{
		Data: &copilot.AssistantTurnStartData{
			InteractionID: &interactionID2,
			TurnID:        turnID2,
		},
	})
	fake.emit(copilot.SessionEvent{
		Data: &copilot.AssistantMessageData{
			Content:       "second request succeeded",
			MessageID:     "assistant-message-2",
			InteractionID: &interactionID2,
			TurnID:        &turnID2,
		},
	})
	fake.emit(copilot.SessionEvent{
		Data: &copilot.AssistantTurnEndData{TurnID: turnID2},
	})

	var successCount, errorCount int
	for range 2 {
		result := <-results
		switch {
		case result.err != nil:
			var sessionErr *CopilotSessionError
			if !errors.As(result.err, &sessionErr) {
				t.Fatalf("error = %v, want CopilotSessionError", result.err)
			}
			if sessionErr.Message != "first request failed" {
				t.Errorf("error message = %q, want %q", sessionErr.Message, "first request failed")
			}
			errorCount++
		case result.event != nil:
			data, ok := result.event.Data.(*copilot.AssistantMessageData)
			if !ok || data.Content != "second request succeeded" {
				t.Errorf("event = %#v, want second request response", result.event)
			}
			successCount++
		default:
			t.Fatal("result has neither event nor error")
		}
	}
	if successCount != 1 || errorCount != 1 {
		t.Errorf("successes = %d, errors = %d, want 1 each", successCount, errorCount)
	}
}

func TestCopilotSendDispatcherCancellationDoesNotHangWithoutTerminalEvent(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	sent := make(chan struct{})
	fake.send = func(context.Context, copilot.MessageOptions) (string, error) {
		fake.emit(copilot.SessionEvent{ID: "message-1", Data: &copilot.UserMessageData{}})
		close(sent)
		return "message-1", nil
	}

	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	dispatcher.abortWait = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := dispatcher.sendAndWait(ctx, copilot.MessageOptions{Prompt: "prompt"}, func() {})
		result <- err
	}()
	<-sent
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sendAndWait() did not return after cancellation")
	}
}

func TestCopilotSendDispatcherRemovesAllProviderIDMappings(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	request := &copilotSendRequest{
		messageIDs:        make(map[string]struct{}),
		serviceRequestIDs: make(map[string]struct{}),
		providerCallIDs:   make(map[string]struct{}),
		result:            make(chan copilotSendResult, 1),
	}
	dispatcher.add(request)

	dispatcher.mu.Lock()
	dispatcher.bindProviderIDsLocked(request, "service-1", "provider-1")
	dispatcher.bindProviderIDsLocked(request, "service-2", "provider-2")
	dispatcher.completeLocked(request, copilotSendResult{})
	if len(dispatcher.byServiceRequestID) != 0 {
		t.Errorf("service request mappings = %v, want empty", dispatcher.byServiceRequestID)
	}
	if len(dispatcher.byProviderCallID) != 0 {
		t.Errorf("provider call mappings = %v, want empty", dispatcher.byProviderCallID)
	}
	dispatcher.mu.Unlock()
}

func TestCopilotSendDispatcherRejectsAmbiguousError(t *testing.T) {
	fake := &fakeCopilotSendSession{}
	sent := make(chan struct{}, 2)
	var sendMu sync.Mutex
	sendCount := 0
	fake.send = func(context.Context, copilot.MessageOptions) (string, error) {
		sendMu.Lock()
		sendCount++
		id := fmt.Sprintf("message-%d", sendCount)
		sendMu.Unlock()
		fake.emit(copilot.SessionEvent{ID: id, Data: &copilot.UserMessageData{}})
		sent <- struct{}{}
		return id, nil
	}

	dispatcher := newCopilotSendDispatcher(fake, "github-copilot")
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := dispatcher.sendAndWait(t.Context(), copilot.MessageOptions{Prompt: "prompt"}, func() {})
			results <- err
		}()
	}
	<-sent
	<-sent
	fake.emit(copilot.SessionEvent{
		Data: &copilot.SessionErrorData{
			ErrorType: CopilotSessionErrorTypeRateLimit,
			Message:   "request rate limited",
		},
	})

	for range 2 {
		err := <-results
		if err == nil || !strings.Contains(err.Error(), "uncorrelated error while 2 requests were pending") {
			t.Errorf("error = %v, want ambiguous correlation error", err)
		}
		var sessionErr *CopilotSessionError
		if errors.As(err, &sessionErr) {
			t.Errorf("errors.As() = true with %#v, want uncorrelated error", sessionErr)
		}
	}
}
