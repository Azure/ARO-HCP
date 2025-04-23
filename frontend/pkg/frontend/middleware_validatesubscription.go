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
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/tracing"
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
	logger := LoggerFromContext(ctx)

	dbClient, err := DBClientFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(w)
		return
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
	subscription, err := dbClient.GetSubscriptionDoc(ctx, subscriptionId)
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
		if subscription.Properties != nil &&
			subscription.Properties.TenantId != nil {
			r.Header.Set(
				arm.HeaderNameHomeTenantID,
				*subscription.Properties.TenantId)
		}
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		tracing.SubscriptionStateKey.String(string(subscription.State)),
	)

	switch subscription.State {
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
				subscription.State)
			return
		}
		next(w, r)
	case arm.SubscriptionStateDeleted:
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidSubscriptionState, "",
			InvalidSubscriptionStateMessage,
			subscription.State)
	default:
		logger.Error(fmt.Sprintf("unsupported subscription state %q", subscription.State))
		arm.WriteInternalServerError(w)
	}
}
