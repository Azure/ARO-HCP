package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareValidateSubscription(t *testing.T) {
	subscriptionId := "1234-5678"
	defaultRequestPath := fmt.Sprintf("subscriptions/%s/resourceGroups/xyz", subscriptionId)
	cache := NewCache()
	middleware := NewSubscriptionStateMuxValidator(cache)

	tests := []struct {
		name           string
		subscriptionId string
		cachedState    arm.RegistrationState
		expectedState  arm.RegistrationState
		httpMethod     string
		requestPath    string
		expectedError  *arm.CloudError
	}{
		{
			name:          "subscription is already registered",
			cachedState:   arm.Registered,
			expectedState: arm.Registered,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is missing from path",
			httpMethod:  http.MethodGet,
			cachedState: arm.Registered,
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
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is deleted",
			cachedState: arm.Deleted,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.Deleted),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is unregistered",
			cachedState: arm.Unregistered,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:          "subscription is suspended - GET is allowed",
			cachedState:   arm.Suspended,
			expectedState: arm.Suspended,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - GET is allowed",
			cachedState:   arm.Warned,
			expectedState: arm.Warned,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - DELETE is allowed",
			cachedState:   arm.Warned,
			expectedState: arm.Warned,
			httpMethod:    http.MethodDelete,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is warned - PUT is not allowed",
			cachedState: arm.Warned,
			httpMethod:  http.MethodPut,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.Warned),
				},
			},
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is suspended - POST is not allowed",
			cachedState: arm.Suspended,
			httpMethod:  http.MethodPost,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.Suspended),
				},
			},
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is suspended - PATCH is not allowed",
			cachedState: arm.Suspended,
			httpMethod:  http.MethodPatch,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.Suspended),
				},
			},
			requestPath: defaultRequestPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if tt.cachedState != "" {
				cache.SetSubscription(subscriptionId, &arm.Subscription{State: tt.cachedState})
			}

			writer := httptest.NewRecorder()

			request, err := http.NewRequest(tt.httpMethod, tt.requestPath, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Add a logger to the context so parsing errors will be logged.
			ctx := ContextWithLogger(request.Context(), slog.Default())
			request = request.WithContext(ctx)
			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
			}
			if tt.requestPath == defaultRequestPath {
				request.SetPathValue(PathSegmentSubscriptionID, subscriptionId)
			}

			middleware.MiddlewareValidateSubscriptionState(writer, request, next)

			// clear the cache for the next test
			cache.DeleteSubscription(subscriptionId)

			result, err := SubscriptionStateFromContext(request.Context())
			if err == nil {
				if !reflect.DeepEqual(result, tt.expectedState) {
					t.Error(cmp.Diff(result, tt.expectedState))
				}
			} else if tt.expectedState != "" {
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
