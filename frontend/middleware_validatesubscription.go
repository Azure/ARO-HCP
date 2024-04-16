package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/subscription"
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
	r = r.WithContext(context.WithValue(r.Context(), ContextKeySubscriptionState, sub.State))
	switch sub.State {
	case subscription.Registered:
		next(w, r)
	case subscription.Unregistered:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
	case subscription.Warned:
		fallthrough // Warned has the same behaviour as Suspended
	case subscription.Suspended:
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			arm.WriteError(w, http.StatusConflict,
				arm.CloudErrorInvalidSubscriptionState, "",
				InvalidSubscriptionStateMessage,
				sub.State)
			return
		}
		next(w, r)
	case subscription.Deleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			sub.State)
	}
}
