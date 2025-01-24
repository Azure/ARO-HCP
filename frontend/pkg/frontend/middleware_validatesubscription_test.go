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
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

func TestMiddlewareValidateSubscription(t *testing.T) {
	subscriptionId := "sub-1234-5678"
	tenantId := "tenant-1234-5678"
	defaultRequestPath := fmt.Sprintf("subscriptions/%s/resourceGroups/xyz", subscriptionId)

	tests := []struct {
		name          string
		cachedState   arm.SubscriptionState
		expectedState arm.SubscriptionState
		httpMethod    string
		requestPath   string
		expectedError *arm.CloudError
	}{
		{
			name:          "subscription is already registered",
			cachedState:   arm.SubscriptionStateRegistered,
			expectedState: arm.SubscriptionStateRegistered,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is missing from path",
			cachedState: arm.SubscriptionStateRegistered,
			httpMethod:  http.MethodGet,
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
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is deleted",
			cachedState: arm.SubscriptionStateDeleted,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.SubscriptionStateDeleted),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:        "subscription is unregistered",
			cachedState: arm.SubscriptionStateUnregistered,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusBadRequest,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(UnregisteredSubscriptionStateMessage, subscriptionId),
				},
			},
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
		},
		{
			name:          "subscription is suspended - GET is allowed",
			cachedState:   arm.SubscriptionStateSuspended,
			expectedState: arm.SubscriptionStateSuspended,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - GET is allowed",
			cachedState:   arm.SubscriptionStateWarned,
			expectedState: arm.SubscriptionStateWarned,
			httpMethod:    http.MethodGet,
			requestPath:   defaultRequestPath,
		},
		{
			name:          "subscription is warned - DELETE is allowed",
			cachedState:   arm.SubscriptionStateWarned,
			expectedState: arm.SubscriptionStateWarned,
			httpMethod:    http.MethodDelete,
			requestPath:   defaultRequestPath,
		},
		{
			name:        "subscription is warned - PUT is not allowed",
			cachedState: arm.SubscriptionStateWarned,
			httpMethod:  http.MethodPut,
			requestPath: defaultRequestPath,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.SubscriptionStateWarned),
				},
			},
		},
		{
			name:        "subscription is suspended - POST is not allowed",
			cachedState: arm.SubscriptionStateSuspended,
			httpMethod:  http.MethodPost,
			requestPath: defaultRequestPath,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.SubscriptionStateSuspended),
				},
			},
		},
		{
			name:        "subscription is suspended - PATCH is not allowed",
			cachedState: arm.SubscriptionStateSuspended,
			httpMethod:  http.MethodPatch,
			requestPath: defaultRequestPath,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusConflict,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidSubscriptionState,
					Message: fmt.Sprintf(InvalidSubscriptionStateMessage, arm.SubscriptionStateSuspended),
				},
			},
		},
		{
			name:        "subscription state value is invalid",
			cachedState: arm.SubscriptionState("Invalid"),
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
			expectedError: &arm.CloudError{
				StatusCode: http.StatusInternalServerError,
				CloudErrorBody: &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInternalServerError,
					Message: "Internal server error.",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)

			var doc *database.SubscriptionDocument

			if tt.cachedState != "" {
				doc = &database.SubscriptionDocument{
					BaseDocument: database.BaseDocument{
						ID: subscriptionId,
					},
					Subscription: &arm.Subscription{
						State: tt.cachedState,
						Properties: &arm.SubscriptionProperties{
							TenantId: &tenantId,
						},
					},
				}
			}

			writer := httptest.NewRecorder()

			request, err := http.NewRequest(tt.httpMethod, tt.requestPath, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Add a logger to the context so parsing errors will be logged.
			ctx := request.Context()
			ctx = ContextWithLogger(ctx, slog.Default())
			ctx = ContextWithDBClient(ctx, mockDBClient)
			request = request.WithContext(ctx)
			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
			}
			if tt.requestPath == defaultRequestPath {
				request.SetPathValue(PathSegmentSubscriptionID, subscriptionId)
				mockDBClient.EXPECT().
					GetSubscriptionDoc(gomock.Any(), subscriptionId).
					Return(getMockDBDoc(doc)) // defined in frontend_test.go
			}

			MiddlewareValidateSubscriptionState(writer, request, next)

			if tt.expectedError != nil {
				var actualError *arm.CloudError
				body, _ := io.ReadAll(http.MaxBytesReader(writer, writer.Result().Body, 4*megabyte))
				_ = json.Unmarshal(body, &actualError)
				if (writer.Result().StatusCode != tt.expectedError.StatusCode) || actualError.Code != tt.expectedError.Code || actualError.Message != tt.expectedError.Message {
					t.Errorf("unexpected CloudError, wanted %v, got %v", tt.expectedError, actualError)
				}
			} else {
				if doc.Subscription.State != tt.expectedState {
					t.Error(cmp.Diff(doc.Subscription.State, tt.expectedState))
				}
			}
		})
	}

	t.Run("nil DB client in the context", func(t *testing.T) {
		writer := httptest.NewRecorder()

		request, err := http.NewRequest(http.MethodGet, defaultRequestPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.SetPathValue(PathSegmentSubscriptionID, subscriptionId)

		ctx := request.Context()
		ctx = ContextWithLogger(ctx, slog.Default())
		request = request.WithContext(ctx)

		next := func(w http.ResponseWriter, r *http.Request) {}
		MiddlewareValidateSubscriptionState(writer, request, next)
		if writer.Code != http.StatusInternalServerError {
			t.Errorf("expected status code %d, got %d", http.StatusInternalServerError, writer.Code)
		}
	})
}
