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

type SubscriptionStateMuxValidator struct {
	cache *Cache
}

func NewSubscriptionStateMuxValidator(c *Cache) *SubscriptionStateMuxValidator {
	return &SubscriptionStateMuxValidator{
		cache: c,
	}
}

func (s *SubscriptionStateMuxValidator) MiddlewareValidateSubscriptionState(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	subscriptionId := r.PathValue(strings.ToLower(PathSegmentSubscriptionID))
	sub, exists := s.cache.GetSubscription(subscriptionId)
	if subscriptionId == "" {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidParameter, "",
			"The request is missing required parameter '%s'.",
			PathSegmentSubscriptionID)
	} else if !exists {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			"The subscription %s is not registered for this provider %s. Please re-register the subscription",
			subscriptionId, api.ProviderNamespace)
	} else if exists {
		r = r.WithContext(context.WithValue(r.Context(), ContextKeySubscriptionState, sub.State))
		switch sub.State {
		case subscription.Registered:
			next(w, r)
		case subscription.Unregistered:
			arm.WriteError(
				w, http.StatusBadRequest,
				arm.CloudErrorInvalidSubscriptionState, "",
				"The subscription %s is not registered for this provider %s. Please re-register the subscription",
				subscriptionId, api.ProviderNamespace)
		case subscription.Warned:
			// TODO: check request method
			next(w, r)
		case subscription.Suspended:
			// TODO: check request method
			next(w, r)
		case subscription.Deleted:
			arm.WriteError(
				w, http.StatusBadRequest,
				arm.CloudErrorInvalidSubscriptionState, "",
				"The subscription %s is deleted and cannot be used to interact with %s",
				subscriptionId, api.ProviderNamespace)
		}
	}
}
