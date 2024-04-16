package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/subscription"
)

const (
	UnregisteredSubscriptionStateMessage = "The subscription %s is not registered for this provider %s. Please re-register the subscription."
	SubscriptionMissingMessage           = "The request is missing required parameter '%s'."
	InvalidSubscriptionStateMessage      = "The subscription %s is in %s state, please ensure subscription is eligible to use the provider %s."
	DeletedSubscriptionMessage           = "The subscription %s is deleted and cannot be used to interact with %s."
)

type SubscriptionStateMuxValidator struct {
	cache *Cache
}

func NewSubscriptionStateMuxValidator(c *Cache) *SubscriptionStateMuxValidator {
	return &SubscriptionStateMuxValidator{
		cache: c,
	}
}

// MiddlewhareValidateSubscriptionState validates the state of the subscription as outlined by
// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/subscription-lifecycle-api-reference.md
func (s *SubscriptionStateMuxValidator) MiddlewareValidateSubscriptionState(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	subscriptionId := r.PathValue(strings.ToLower(PathSegmentSubscriptionID))
	sub, exists := s.cache.GetSubscription(subscriptionId)
	if subscriptionId == "" {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidParameter, "",
			SubscriptionMissingMessage,
			PathSegmentSubscriptionID)
		return
	}

	if !exists {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId, api.ProviderNamespace)
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
			subscriptionId, api.ProviderNamespace)
	case subscription.Warned:
		fallthrough // Warned has the same behaviour as Suspended
	case subscription.Suspended:
		if r.Method == http.MethodGet || r.Method == http.MethodDelete {
			next(w, r)
		} else if r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodPost {
			arm.WriteError(w, http.StatusConflict,
				arm.CloudErrorInvalidSubscriptionState, "", InvalidSubscriptionStateMessage, subscriptionId, sub.State, api.ProviderNamespace)
		}
	case subscription.Deleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			DeletedSubscriptionMessage,
			subscriptionId, api.ProviderNamespace)
	}
}
