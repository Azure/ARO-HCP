package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	UnregisteredSubscriptionStateMessage = "Request is not allowed in unregistered subscription '%s'."
	InvalidSubscriptionStateMessage      = "Request is not allowed in subscription in state '%s'."
	SubscriptionMissingMessage           = "The request is missing required parameter '%s'."
)

type SubscriptionStateMuxValidator struct {
	dbClient database.DBClient
}

func NewSubscriptionStateMuxValidator(dbClient database.DBClient) *SubscriptionStateMuxValidator {
	return &SubscriptionStateMuxValidator{
		dbClient: dbClient,
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

	// TODO: Ideally, we don't want to have to hit the database in this middleware
	// Currently, we are using the database to retrieve the subscription's tenantID and state
	sub, err := s.dbClient.GetSubscriptionDoc(r.Context(), subscriptionId)
	if err != nil {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
		return
	}

	ctx := ContextWithSubscription(r.Context(), *sub.Subscription)
	r = r.WithContext(ctx)
	switch sub.Subscription.State {
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
				sub.Subscription.State)
			return
		}
		next(w, r)
	case arm.Deleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			sub.Subscription.State)
	}
}
