package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	UnregisteredSubscriptionStateMessage = "Request is not allowed in unregistered subscription '%s'."
	InvalidSubscriptionStateMessage      = "Request is not allowed in subscription in state '%s'."
	SubscriptionMissingMessage           = "The request is missing required parameter '%s'."
)

type SubscriptionStateMuxValidator struct {
	cache *Cache
}

func NewSubscriptionStateMuxValidator(c *Cache) *SubscriptionStateMuxValidator {
	return &SubscriptionStateMuxValidator{
		cache: c,
	}
}

// MiddlewareValidateSubscriptionState validates the state of the subscription as outlined by
// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/subscription-lifecycle-api-reference.md
func (s *SubscriptionStateMuxValidator) MiddlewareValidateSubscriptionState(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	subscriptionId := r.PathValue(PathSegmentSubscriptionID)
	if subscriptionId == "" {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidParameter, "",
			SubscriptionMissingMessage,
			PathSegmentSubscriptionID)
		return
	}

	sub, exists := s.cache.GetSubscription(subscriptionId)

	if !exists {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
		return
	}

	// the subscription exists, store its current state as context
	ctx := ContextWithSubscriptionState(r.Context(), sub.State)
	r = r.WithContext(ctx)
	switch sub.State {
	case arm.Registered:
		next(w, r)
	case arm.Unregistered:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
	case arm.Warned, arm.Suspended:
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			arm.WriteError(w, http.StatusConflict,
				arm.CloudErrorInvalidSubscriptionState, "",
				InvalidSubscriptionStateMessage,
				sub.State)
			return
		}
		next(w, r)
	case arm.Deleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			sub.State)
	}
}
