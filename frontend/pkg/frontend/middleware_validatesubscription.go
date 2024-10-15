package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/frontend/pkg/config"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	UnregisteredSubscriptionStateMessage = "Request is not allowed in unregistered subscription '%s'."
	InvalidSubscriptionStateMessage      = "Request is not allowed in subscription in state '%s'."
	SubscriptionMissingMessage           = "The request is missing required parameter '%s'."
)

// MiddlewareValidateSubscriptionState validates the state of the subscription as outlined by
// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/subscription-lifecycle-api-reference.md
func MiddlewareValidateSubscriptionState(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()

	logger, err := LoggerFromContext(ctx)
	if err != nil {
		config.DefaultLogger().Error(err.Error())
		arm.WriteInternalServerError(w)
		return
	}

	dbClient, err := DBClientFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(w)
	}

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
	sub, err := dbClient.GetSubscriptionDoc(ctx, subscriptionId)
	if err != nil {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
		return
	}

	// For subscription-scoped requests, ARM will provide the tenant ID
	// in a "x-ms-home-tenant-id" header. But in test environments this
	// header may not be present, in which case we can try to fudge it
	// from the SubscriptionDocument.
	if r.Header.Get(arm.HeaderNameHomeTenantID) == "" {
		if sub.Subscription != nil &&
			sub.Subscription.Properties != nil &&
			sub.Subscription.Properties.TenantId != nil {
			r.Header.Set(
				arm.HeaderNameHomeTenantID,
				*sub.Subscription.Properties.TenantId)
		}
	}

	switch sub.Subscription.State {
	case arm.SubscriptionStateRegistered:
		next(w, r)
	case arm.SubscriptionStateUnregistered:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
	case arm.SubscriptionStateWarned, arm.SubscriptionStateSuspended:
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			arm.WriteError(w, http.StatusConflict,
				arm.CloudErrorCodeInvalidSubscriptionState, "",
				InvalidSubscriptionStateMessage,
				sub.Subscription.State)
			return
		}
		next(w, r)
	case arm.SubscriptionStateDeleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			sub.Subscription.State)
	}
}
