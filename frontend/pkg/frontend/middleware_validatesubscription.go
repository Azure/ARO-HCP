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
	"net/http"

	"go.opentelemetry.io/otel/trace"

	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	UnregisteredSubscriptionStateMessage = "Request is not allowed in unregistered subscription '%s'."
	InvalidSubscriptionStateMessage      = "Request is not allowed in subscription in state '%s'."
	SubscriptionMissingMessage           = "The request is missing required parameter '%s'."
)

type middlewareValidateSubscriptionState struct {
	resourcesDBClient database.ResourcesDBClient
}

func newMiddlewareValidateSubscriptionState(resourcesDBClient database.ResourcesDBClient) *middlewareValidateSubscriptionState {
	return &middlewareValidateSubscriptionState{
		resourcesDBClient: resourcesDBClient,
	}
}

// handleRequest validates the state of the subscription as outlined by
// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/subscription-lifecycle-api-reference.md
func (h *middlewareValidateSubscriptionState) handleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	subscriptionId := r.PathValue(PathSegmentSubscriptionID)
	if subscriptionId == "" {
		armresourcesapi.WriteError(
			w, http.StatusBadRequest,
			armresourcesapi.CloudErrorCodeInvalidParameter, "",
			SubscriptionMissingMessage,
			PathSegmentSubscriptionID)
		return
	}

	subscription, err := h.resourcesDBClient.Subscriptions().Get(ctx, subscriptionId)
	if err != nil {
		logger.Error(err, "failed to get subscription document", "subscriptionId", subscriptionId)

		// subscription not found, treat as unregistered
		if database.IsNotFoundError(err) {
			armresourcesapi.WriteError(
				w, http.StatusBadRequest,
				armresourcesapi.CloudErrorCodeInvalidSubscriptionState, "",
				UnregisteredSubscriptionStateMessage,
				subscriptionId)
			return
		}
		armresourcesapi.WriteInternalServerError(w)
		return
	}

	// For subscription-scoped requests, ARM will provide the tenant ID
	// in a "x-ms-home-tenant-id" header. But in test environments this
	// header may not be present, in which case we can try to fudge it
	// from the SubscriptionDocument.
	if r.Header.Get(armresourcesapi.HeaderNameHomeTenantID) == "" {
		if subscription != nil &&
			subscription.Properties != nil &&
			subscription.Properties.TenantId != nil {
			r.Header.Set(
				armresourcesapi.HeaderNameHomeTenantID,
				*subscription.Properties.TenantId)
		}
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		tracing.SubscriptionStateKey.String(string(subscription.State)),
	)

	// Stash the subscription for REST handlers.
	ctx = ContextWithSubscription(ctx, subscription)
	r = r.WithContext(ctx)

	switch subscription.State {
	case armresourcesapi.SubscriptionStateRegistered:
		next(w, r)
	case armresourcesapi.SubscriptionStateUnregistered:
		logger.Error(nil, "subscription document indicates unregistered", "subscriptionId", subscriptionId)
		armresourcesapi.WriteError(
			w, http.StatusBadRequest,
			armresourcesapi.CloudErrorCodeInvalidSubscriptionState, "",
			UnregisteredSubscriptionStateMessage,
			subscriptionId)
	case armresourcesapi.SubscriptionStateWarned, armresourcesapi.SubscriptionStateSuspended:
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			logger.Error(nil, "subscription document indicates restricted state", "subscriptionId", subscriptionId, "state", subscription.State)
			armresourcesapi.WriteError(w, http.StatusConflict,
				armresourcesapi.CloudErrorCodeInvalidSubscriptionState, "",
				InvalidSubscriptionStateMessage,
				subscription.State)
			return
		}
		next(w, r)
	case armresourcesapi.SubscriptionStateDeleted:
		logger.Error(nil, "subscription document indicates deleted", "subscriptionId", subscriptionId)
		armresourcesapi.WriteError(
			w, http.StatusBadRequest,
			armresourcesapi.CloudErrorCodeInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			subscription.State)
	default:
		logger.Error(nil, "unsupported subscription state", "subscriptionState", subscription.State)
		armresourcesapi.WriteInternalServerError(w)
	}
}
