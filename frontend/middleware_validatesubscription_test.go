package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/subscription"
)

func TestMiddlewareValidateSubscription(t *testing.T) {
	subscriptionId := "1234-5678"
	defaultRequestPath := fmt.Sprintf("subscriptions/%s/resourceGroups/xyz", subscriptionId)
	cache := NewCache()
	middleware := NewSubscriptionStateMuxValidator(cache)

	tests := []struct {
		name           string
		subscriptionId string
		cachedState    subscription.RegistrationState
		expectedState  subscription.RegistrationState
		httpMethod     string
		requestPath    string
		expectedError  *arm.CloudError
	}{
		{
			name:          "subscription is already registered",
			cachedState:   subscription.Registered,
			expectedState: subscription.Registered,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is missing from path",
			httpMethod:  http.MethodGet,
			cachedState: subscription.Registered,
			requestPath: "/resourceGroups/abc",
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidParameter,
					Message: fmt.Sprintf(SubscriptionMissingMessage, PathSegmentSubscriptionID),
				},
			},
		},
		{
			name: "subscription is not found",
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId, api.ProviderNamespace),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is deleted",
			cachedState: subscription.Deleted,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(DeletedSubscriptionMessage, subscriptionId, api.ProviderNamespace),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is unregistered",
			cachedState: subscription.Unregistered,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId, api.ProviderNamespace),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:          "subscription is suspended - GET is allowed",
			cachedState:   subscription.Suspended,
			expectedState: subscription.Suspended,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - GET is allowed",
			cachedState:   subscription.Warned,
			expectedState: subscription.Warned,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - DELETE is allowed",
			cachedState:   subscription.Warned,
			expectedState: subscription.Warned,
			httpMethod:    http.MethodDelete,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is warned - PUT is not allowed",
			cachedState: subscription.Warned,
			httpMethod:  http.MethodPut,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, subscriptionId, subscription.Warned, api.ProviderNamespace),
				},
			},
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is suspended - POST is not allowed",
			cachedState: subscription.Suspended,
			httpMethod:  http.MethodPost,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, subscriptionId, subscription.Suspended, api.ProviderNamespace),
				},
			},
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is suspended - PATCH is not allowed",
			cachedState: subscription.Suspended,
			httpMethod:  http.MethodPatch,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, subscriptionId, subscription.Suspended, api.ProviderNamespace),
				},
			},
			requestPath: defaultRequestPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if tt.cachedState != "" {
				cache.SetSubscription(subscriptionId, &subscription.Subscription{State: tt.cachedState})
			}

			writer := httptest.NewRecorder()

			request, err := http.NewRequest(tt.httpMethod, tt.requestPath, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Add a logger to the context so parsing errors will be logged.
			request = request.WithContext(context.WithValue(request.Context(), ContextKeyLogger, slog.Default()))
			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
			}
			if tt.requestPath == defaultRequestPath {
				request.SetPathValue(strings.ToLower(PathSegmentSubscriptionID), subscriptionId)
			}

			middleware.MiddlewareValidateSubscriptionState(writer, request, next)

			// clear the cache for the next test
			cache.DeleteSubscription(subscriptionId)

			result, ok := request.Context().Value(ContextKeySubscriptionState).(subscription.RegistrationState)
			if ok {
				if !reflect.DeepEqual(result, tt.expectedState) {
					t.Error(cmp.Diff(result, tt.expectedState))
				}
			}
			if tt.expectedState != "" && !ok {
				t.Errorf("Expected RegistrationState %s in request context", tt.expectedState)
			}
			if tt.expectedError != nil {
				var actualError *arm.CloudError
				body, _ := io.ReadAll(http.MaxBytesReader(writer, writer.Result().Body, 4*megabyte))
				_ = json.Unmarshal(body, &actualError)
				if (writer.Result().StatusCode != tt.expectedError.StatusCode) || actualError.Code != tt.expectedError.Code || actualError.Message != tt.expectedError.Message {
					t.Errorf("unexpected CloudError, wanted %v, got %v", tt.expectedError, actualError)
				}
			}
		})
	}
}
