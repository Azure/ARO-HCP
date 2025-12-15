// Copyright 2025 Microsoft Corporation
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

package frontend

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type middlewareLockSubscription struct {
	dbClient database.DBClient
}

func newMiddlewareLockSubscription(dbClient database.DBClient) *middlewareLockSubscription {
	return &middlewareLockSubscription{
		dbClient: dbClient,
	}
}

// handleRequest this is best effort, not guaranteed correct.  This must not be relied upon for guaranteeing correctness.
func (h *middlewareLockSubscription) handleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	subscriptionID := r.PathValue(PathSegmentSubscriptionID)

	// This may be nil when running "go test".
	lockClient := h.dbClient.GetLockClient()

	if lockClient == nil {
		next(w, r)
	} else {
		// Wait for the default TTL to acquire lock.
		timeout := lockClient.GetDefaultTimeToLive()
		lock, err := lockClient.AcquireLock(ctx, subscriptionID, &timeout)
		if err != nil {
			message := "Failed to acquire lock: "
			if errors.Is(err, context.DeadlineExceeded) {
				message += "timed out"
				lockClient.SetRetryAfterHeader(w.Header())
				arm.WriteError(
					w, http.StatusServiceUnavailable,
					arm.CloudErrorCodeLockContention,
					"/subscriptions/"+subscriptionID, "%s", message)
			} else {
				message += err.Error()
				arm.WriteInternalServerError(w)
			}
			logger.Error(message)
			return
		}
		logger.Info("Acquired lock")

		// Hold the lock until the remaining handlers complete.
		// If we lose the lock the context will be cancelled.
		// TODO this implementation is racy.  If the internal c.RenewLock fails, but does not return quickly then a second lock can be acquired.
		lockedCtx, stop := lockClient.HoldLock(ctx, lock)
		defer func() {
			lock = stop()
			if lock != nil {
				err = lockClient.ReleaseLock(ctx, lock)
				if err == nil {
					logger.Info("Released lock")
				} else {
					// Failure here is non-fatal but still log the error.
					// The lock's TTL ensures it will be released eventually.
					logger.Error(fmt.Sprintf("Failed to release lock: %v", err))
				}
			}
		}()

		r = r.WithContext(lockedCtx)

		next(w, r)
	}
}
