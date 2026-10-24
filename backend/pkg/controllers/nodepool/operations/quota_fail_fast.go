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
	"context"
	"fmt"
	"strings"
	"time"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/quotaerror"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// quotaFailFastWindow is how long a quota error must persist, continuously,
// before the operation fails. It gives the customer's quota increase, and CAPZ's
// own retries, a chance to clear a transient condition. POC value, not tuned.
const quotaFailFastWindow = 5 * time.Minute

// quotaFailFast turns a node pool operation that is stuck on the customer's
// Azure compute quota into a failed operation with the QuotaExceeded error code,
// instead of waiting for the completion deadline to report DeadlineExceeded.
//
// The first seen times live in memory, keyed by operation. A backend restart
// only restarts the window. A nil quotaFailFast leaves every state unchanged.
type quotaFailFast struct {
	clock     utilsclock.PassiveClock
	firstSeen *lru.Cache
}

// firstSeenKey identifies an operation in the in memory first seen caches.
// operation.ResourceID is unique per operation, so a stale entry never matches a newer one.
func firstSeenKey(operation *coreapi.Operation) string {
	return strings.ToLower(operation.ResourceID.String())
}

func newQuotaFailFast(clock utilsclock.PassiveClock) *quotaFailFast {
	return &quotaFailFast{clock: clock, firstSeen: lru.New(100000)}
}

// apply inspects the Hypershift derived state for the operation. While the state
// is non terminal and entirely explained by quota errors it is returned unchanged
// until the window has passed, then replaced by a failed state. Any other state,
// including a quota error that goes away, resets the window.
func (q *quotaFailFast) apply(ctx context.Context, operationID string, state *operationbase.OperationState) *operationbase.OperationState {
	return q.process(ctx, operationID, state, false)
}

// applyPastDeadline is apply for an operation whose completion deadline has passed.
// A quota error present at that point fails the operation as QuotaExceeded at once,
// instead of DeadlineExceeded, so the customer is told the cause and the operation
// is attributed to them.
func (q *quotaFailFast) applyPastDeadline(ctx context.Context, operationID string, state *operationbase.OperationState) *operationbase.OperationState {
	return q.process(ctx, operationID, state, true)
}

func (q *quotaFailFast) process(ctx context.Context, operationID string, state *operationbase.OperationState, pastDeadline bool) *operationbase.OperationState {
	if q == nil || state == nil {
		return state
	}
	if state.ProvisioningState.IsTerminal() {
		q.firstSeen.Remove(operationID)
		return state
	}
	detail, ok := quotaerror.Detect(state.Message)
	if !ok {
		if quotaerror.HasMachineFailures(state.Message) {
			// Makes detection rot visible: a quota failure whose text changed shape lands here.
			utils.LoggerFromContext(ctx).V(1).Info("machine failures were not classified as quota",
				"diagnosticMessage", state.Message)
		}
		q.firstSeen.Remove(operationID)
		return state
	}

	if !pastDeadline {
		first, seen := q.firstSeen.Get(operationID)
		if !seen {
			q.firstSeen.Add(operationID, q.clock.Now())
			return state
		}
		if q.clock.Since(first.(time.Time)) < quotaFailFastWindow {
			return state
		}
	}
	q.firstSeen.Remove(operationID)
	logMessage := "quota error persisted past the fail fast window, failing operation"
	if pastDeadline {
		logMessage = "quota error present at the completion deadline, failing operation"
	}
	utils.LoggerFromContext(ctx).Info(logMessage,
		"window", quotaFailFastWindow.String(),
		"quotaDetail", detail,
		"diagnosticMessage", state.Message)
	message := quotaExceededMessage(detail)
	return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeQuotaExceeded, message, &coreapi.CloudErrorBody{
		Code:    coreapi.CloudErrorCodeQuotaExceeded,
		Message: message,
	})
}

// quotaExceededMessage is persisted on the failed operation and shown to the customer.
func quotaExceededMessage(detail string) string {
	message := "Node pool virtual machines could not be created because the subscription does not have enough Azure compute quota in this region. Increase the vCPU quota for the subscription."
	if len(detail) == 0 {
		return message
	}
	return fmt.Sprintf("%s Details: %s", message, detail)
}
