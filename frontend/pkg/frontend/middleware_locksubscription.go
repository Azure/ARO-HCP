package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

func MiddlewareLockSubscription(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	var lockClient *database.LockClient

	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	dbClient, err := DBClientFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(w)
		return
	}

	subscriptionID := r.PathValue(PathSegmentSubscriptionID)

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		// These methods are read-only and don't require locking.
	default:
		lockClient = dbClient.GetLockClient()
	}

	if lockClient == nil {
		next(w, r)
	} else {
		// Wait for the default TTL to acquire lock.
		timeout := lockClient.GetDefaultTimeToLive()
		lock, err := lockClient.AcquireLock(ctx, subscriptionID, &timeout)
		if err != nil {
			message := fmt.Sprintf("Failed to acquire lock for subscription '%s': ", subscriptionID)
			if errors.Is(err, context.DeadlineExceeded) {
				message += "timed out"
				lockClient.SetRetryAfterHeader(w.Header())
				arm.WriteError(
					w, http.StatusConflict, arm.CloudErrorCodeConflict,
					"/subscriptions/"+subscriptionID, "%s", message)
			} else {
				message += err.Error()
				arm.WriteInternalServerError(w)
			}
			logger.Error(message)
			return
		}
		logger.Info(fmt.Sprintf("Acquired lock for subscription '%s'", subscriptionID))

		// Hold the lock until the remaining handlers complete.
		// If we lose the lock the context will be cancelled.
		lockedCtx, stop := lockClient.HoldLock(ctx, lock)
		r = r.WithContext(lockedCtx)

		next(w, r)

		lock = stop()
		if lock != nil {
			err = lockClient.ReleaseLock(ctx, lock)
			if err == nil {
				logger.Info(fmt.Sprintf("Released lock for subscription '%s'", subscriptionID))
			} else {
				// Failure here is non-fatal but still log the error.
				// The lock's TTL ensures it will be released eventually.
				logger.Error(fmt.Sprintf("Failed to release lock for subscription '%s': %v", subscriptionID, err))
			}
		}
	}
}
