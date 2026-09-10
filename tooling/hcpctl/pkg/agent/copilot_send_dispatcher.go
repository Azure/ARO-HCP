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
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type copilotSendSession interface {
	Send(context.Context, copilot.MessageOptions) (string, error)
	On(copilot.SessionEventHandler) func()
}

type copilotSendResult struct {
	event *copilot.SessionEvent
	err   error
}

type copilotSendRequest struct {
	messageID         string
	interactionID     string
	turnID            string
	messageIDs        map[string]struct{}
	serviceRequestIDs map[string]struct{}
	providerCallIDs   map[string]struct{}
	lastAssistant     *copilot.SessionEvent
	result            chan copilotSendResult
	completed         bool
}

// copilotSendDispatcher owns the session-wide event subscription and routes
// terminal events to the Send call that initiated the corresponding turn.
type copilotSendDispatcher struct {
	inner     copilotSendSession
	provider  string
	abortWait time.Duration

	// Serialize only session.send submission so user.message events can be
	// bound to the request whose RPC is in progress. Requests wait concurrently.
	submitGate chan struct{}

	mu                 sync.Mutex
	requests           []*copilotSendRequest
	byMessageID        map[string]*copilotSendRequest
	byInteractionID    map[string]*copilotSendRequest
	byTurnID           map[string]*copilotSendRequest
	byServiceRequestID map[string]*copilotSendRequest
	byProviderCallID   map[string]*copilotSendRequest
	openTurns          map[string]*copilotSendRequest
}

func newCopilotSendDispatcher(inner copilotSendSession, provider string) *copilotSendDispatcher {
	d := &copilotSendDispatcher{
		inner:              inner,
		provider:           provider,
		abortWait:          5 * time.Second,
		submitGate:         make(chan struct{}, 1),
		byMessageID:        make(map[string]*copilotSendRequest),
		byInteractionID:    make(map[string]*copilotSendRequest),
		byTurnID:           make(map[string]*copilotSendRequest),
		byServiceRequestID: make(map[string]*copilotSendRequest),
		byProviderCallID:   make(map[string]*copilotSendRequest),
		openTurns:          make(map[string]*copilotSendRequest),
	}
	inner.On(d.record)
	return d
}

func (d *copilotSendDispatcher) sendAndWait(
	ctx context.Context,
	options copilot.MessageOptions,
	abort func(),
) (*copilot.SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case d.submitGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseSubmission := func() {
		<-d.submitGate
	}

	request := &copilotSendRequest{
		messageIDs:        make(map[string]struct{}),
		serviceRequestIDs: make(map[string]struct{}),
		providerCallIDs:   make(map[string]struct{}),
		result:            make(chan copilotSendResult, 1),
	}
	d.add(request)

	type sendResult struct {
		messageID string
		err       error
	}
	sent := make(chan sendResult, 1)
	go func() {
		defer utilruntime.HandleCrash()
		messageID, err := d.inner.Send(ctx, options)
		sent <- sendResult{messageID: messageID, err: err}
	}()

	var send sendResult
	cancelled := false
	select {
	case send = <-sent:
	case <-ctx.Done():
		cancelled = true
		abort()
		send = <-sent
	}
	if send.err != nil {
		d.remove(request)
		releaseSubmission()
		if cancelled {
			return nil, fmt.Errorf("copilot session aborted: %w", send.err)
		}
		return nil, fmt.Errorf("copilot session failed to send message: %w", send.err)
	}
	d.bindMessageID(request, send.messageID)
	releaseSubmission()
	if cancelled {
		return d.waitAfterAbort(ctx, request)
	}

	select {
	case result := <-request.result:
		return result.event, result.err
	default:
	}
	select {
	case result := <-request.result:
		return result.event, result.err
	case <-ctx.Done():
		abort()
		return d.waitAfterAbort(ctx, request)
	}
}

func (d *copilotSendDispatcher) waitAfterAbort(ctx context.Context, request *copilotSendRequest) (*copilot.SessionEvent, error) {
	timer := time.NewTimer(d.abortWait)
	defer timer.Stop()
	select {
	case result := <-request.result:
		if result.err != nil {
			return nil, fmt.Errorf("copilot session aborted: %w", result.err)
		}
		return nil, ctx.Err()
	case <-timer.C:
		d.remove(request)
		return nil, ctx.Err()
	}
}

func (d *copilotSendDispatcher) add(request *copilotSendRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, request)
}

func (d *copilotSendDispatcher) bindMessageID(request *copilotSendRequest, messageID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	request.messageID = messageID
	if !request.completed {
		request.messageIDs[messageID] = struct{}{}
		d.byMessageID[messageID] = request
	}
}

func (d *copilotSendDispatcher) remove(request *copilotSendRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removeLocked(request)
}

func (d *copilotSendDispatcher) record(event copilot.SessionEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch data := event.Data.(type) {
	case *copilot.UserMessageData:
		if data.IsAutopilotContinuation != nil && *data.IsAutopilotContinuation {
			return
		}
		request := d.byMessageID[event.ID]
		if request == nil {
			request = d.firstUnboundLocked()
		}
		if request == nil {
			return
		}
		request.messageID = event.ID
		request.messageIDs[event.ID] = struct{}{}
		d.byMessageID[event.ID] = request
		if data.InteractionID != nil {
			request.interactionID = *data.InteractionID
			d.byInteractionID[*data.InteractionID] = request
		}

	case *copilot.AssistantTurnStartData:
		request := d.lookupExactLocked("", "", "", pointerValue(data.InteractionID))
		if request == nil {
			request = d.firstPendingLocked()
		}
		if request == nil {
			return
		}
		request.turnID = data.TurnID
		d.byTurnID[data.TurnID] = request
		if data.InteractionID != nil {
			request.interactionID = *data.InteractionID
			d.byInteractionID[*data.InteractionID] = request
		}
		d.openTurns[data.TurnID] = request

	case *copilot.AssistantMessageData:
		request := d.lookupExactLocked(
			pointerValue(data.ServiceRequestID),
			pointerValue(data.RequestID),
			pointerValue(data.TurnID),
			pointerValue(data.InteractionID),
		)
		if request == nil {
			request = d.onlyOpenTurnLocked()
		}
		if request == nil {
			return
		}
		d.bindProviderIDsLocked(request, pointerValue(data.ServiceRequestID), pointerValue(data.RequestID))
		eventCopy := event
		request.lastAssistant = &eventCopy

	case *copilot.AssistantUsageData:
		if request := d.onlyOpenTurnLocked(); request != nil {
			d.bindProviderIDsLocked(request, pointerValue(data.ServiceRequestID), pointerValue(data.ProviderCallID))
		}

	case *copilot.ModelCallFailureData:
		if request := d.onlyOpenTurnLocked(); request != nil {
			d.bindProviderIDsLocked(request, pointerValue(data.ServiceRequestID), pointerValue(data.ProviderCallID))
		}

	case *copilot.SessionErrorData:
		request := d.lookupExactLocked(
			pointerValue(data.ServiceRequestID),
			pointerValue(data.ProviderCallID),
			"",
			"",
		)
		if request == nil {
			d.failAmbiguousLocked(data)
			return
		}
		cause := fmt.Errorf("session error: %s", data.Message)
		d.completeLocked(request, copilotSendResult{
			err: wrapCopilotSessionErrorData(d.provider, data, cause),
		})

	case *copilot.AssistantTurnEndData:
		request := d.byTurnID[data.TurnID]
		if request == nil {
			return
		}
		delete(d.openTurns, data.TurnID)
		if request.lastAssistant == nil {
			d.completeLocked(request, copilotSendResult{
				err: errors.New("copilot session returned no response"),
			})
			return
		}
		d.completeLocked(request, copilotSendResult{event: request.lastAssistant})

	case *copilot.SessionIdleData:
		pending := append([]*copilotSendRequest(nil), d.requests...)
		for _, request := range pending {
			if request.completed {
				continue
			}
			if request.lastAssistant == nil {
				d.completeLocked(request, copilotSendResult{
					err: errors.New("copilot session returned no response"),
				})
				continue
			}
			d.completeLocked(request, copilotSendResult{event: request.lastAssistant})
		}
	}
}

func (d *copilotSendDispatcher) lookupExactLocked(serviceRequestID, providerCallID, turnID, interactionID string) *copilotSendRequest {
	if serviceRequestID != "" {
		if request := d.byServiceRequestID[serviceRequestID]; request != nil && !request.completed {
			return request
		}
	}
	if providerCallID != "" {
		if request := d.byProviderCallID[providerCallID]; request != nil && !request.completed {
			return request
		}
	}
	if turnID != "" {
		if request := d.byTurnID[turnID]; request != nil && !request.completed {
			return request
		}
	}
	if interactionID != "" {
		if request := d.byInteractionID[interactionID]; request != nil && !request.completed {
			return request
		}
	}
	return nil
}

func (d *copilotSendDispatcher) bindProviderIDsLocked(request *copilotSendRequest, serviceRequestID, providerCallID string) {
	if serviceRequestID != "" {
		request.serviceRequestIDs[serviceRequestID] = struct{}{}
		d.byServiceRequestID[serviceRequestID] = request
	}
	if providerCallID != "" {
		request.providerCallIDs[providerCallID] = struct{}{}
		d.byProviderCallID[providerCallID] = request
	}
}

func (d *copilotSendDispatcher) firstUnboundLocked() *copilotSendRequest {
	for _, request := range d.requests {
		if !request.completed && request.messageID == "" {
			return request
		}
	}
	return nil
}

func (d *copilotSendDispatcher) firstPendingLocked() *copilotSendRequest {
	for _, request := range d.requests {
		if !request.completed && request.turnID == "" {
			return request
		}
	}
	return nil
}

func (d *copilotSendDispatcher) onlyOpenTurnLocked() *copilotSendRequest {
	var found *copilotSendRequest
	for _, request := range d.openTurns {
		if request.completed {
			continue
		}
		if found != nil && found != request {
			return nil
		}
		found = request
	}
	return found
}

func (d *copilotSendDispatcher) failAmbiguousLocked(data *copilot.SessionErrorData) {
	pending := make([]*copilotSendRequest, 0, len(d.requests))
	for _, request := range d.requests {
		if !request.completed {
			pending = append(pending, request)
		}
	}
	if len(pending) == 1 {
		cause := fmt.Errorf("session error: %s", data.Message)
		d.completeLocked(pending[0], copilotSendResult{
			err: wrapCopilotSessionErrorData(d.provider, data, cause),
		})
		return
	}

	err := fmt.Errorf(
		"copilot session emitted an uncorrelated error while %d requests were pending: %s",
		len(pending),
		data.Message,
	)
	for _, request := range pending {
		d.completeLocked(request, copilotSendResult{err: err})
	}
}

func (d *copilotSendDispatcher) completeLocked(request *copilotSendRequest, result copilotSendResult) {
	if request.completed {
		return
	}
	request.completed = true
	d.removeLocked(request)
	request.result <- result
}

func (d *copilotSendDispatcher) removeLocked(request *copilotSendRequest) {
	for messageID := range request.messageIDs {
		delete(d.byMessageID, messageID)
	}
	delete(d.byInteractionID, request.interactionID)
	delete(d.byTurnID, request.turnID)
	delete(d.openTurns, request.turnID)
	for serviceRequestID := range request.serviceRequestIDs {
		delete(d.byServiceRequestID, serviceRequestID)
	}
	for providerCallID := range request.providerCallIDs {
		delete(d.byProviderCallID, providerCallID)
	}
	for i, existing := range d.requests {
		if existing == request {
			d.requests = append(d.requests[:i], d.requests[i+1:]...)
			break
		}
	}
}

func (d *copilotSendDispatcher) failAll(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pending := append([]*copilotSendRequest(nil), d.requests...)
	for _, request := range pending {
		d.completeLocked(request, copilotSendResult{err: err})
	}
}

func pointerValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
