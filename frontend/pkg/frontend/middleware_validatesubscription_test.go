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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
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
		cosmosdbError error
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
		{
			name:        "cosmosdb error returns internal server error",
			httpMethod:  http.MethodGet,
			requestPath: defaultRequestPath,
			cosmosdbError: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
				ErrorCode:  "CosmosDB is down",
			},
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
			mockDBClient := database.NewMockDBClient(ctrl)
			mockSubscriptionCRUD := database.NewMockSubscriptionCRUD(ctrl)

			var subscription *arm.Subscription

			if tt.cachedState != "" {
				subscription = &arm.Subscription{
					State: tt.cachedState,
					Properties: &arm.SubscriptionProperties{
						TenantId: &tenantId,
					},
				}
			}

			writer := httptest.NewRecorder()

			request, err := http.NewRequest(tt.httpMethod, tt.requestPath, nil)
			assert.NoError(t, err)

			// Add a logger to the context so parsing errors will be logged.
			ctx := request.Context()
			ctx = utils.ContextWithLogger(ctx, slog.Default())
			ctx, sr := initSpanRecorder(ctx)
			request = request.WithContext(ctx)

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
			}

			if tt.requestPath == defaultRequestPath {
				request.SetPathValue(PathSegmentSubscriptionID, subscriptionId)
				mockDBClient.EXPECT().
					Subscriptions().
					Return(mockSubscriptionCRUD)

				if tt.cosmosdbError != nil {
					mockSubscriptionCRUD.EXPECT().
						Get(gomock.Any(), subscriptionId).
						Return(nil, tt.cosmosdbError)
				} else {
					mockSubscriptionCRUD.EXPECT().
						Get(gomock.Any(), subscriptionId).
						Return(getMockDBDoc(subscription)) // defined in frontend_test.go
				}
			}

			newMiddlewareValidateSubscriptionState(mockDBClient).handleRequest(writer, request, next)

			res := writer.Result()
			if tt.expectedError != nil {
				var actualError arm.CloudError
				err = json.NewDecoder(res.Body).Decode(&actualError)
				assert.NoError(t, err)

				assert.Equal(t, tt.expectedError.StatusCode, res.StatusCode)
				assert.Equal(t, tt.expectedError.Code, actualError.Code)
				assert.Equal(t, tt.expectedError.Message, actualError.Message)
				return
			}

			assert.Equal(t, tt.expectedState, subscription.State)

			// Check that the attributes have been added to the span too.
			ss := sr.collect()
			require.Len(t, ss, 1)
			span := ss[0]
			equalSpanAttributes(t, span, map[string]string{"aro.subscription.state": string(subscription.State)})
		})
	}

	t.Run("nil DB client in the context", func(t *testing.T) {
		writer := httptest.NewRecorder()
		writer.Code = 0 // set the zero value so we can distinguish between a written result and default

		request, err := http.NewRequest(http.MethodGet, defaultRequestPath, nil)
		assert.NoError(t, err)
		request.SetPathValue(PathSegmentSubscriptionID, subscriptionId)

		ctx := request.Context()
		ctx = utils.ContextWithLogger(ctx, slog.Default())
		request = request.WithContext(ctx)

		next := func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("should not reach this call")
		}

		// this is a coding error (client cannot trigger this) from which the request cannot recover.  A panic appropriate.
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("Recovery as expected", r)
				}
			}()

			newMiddlewareValidateSubscriptionState(nil).handleRequest(writer, request, next)
		}()

		// 0 indicates that no result was written by this handler.  In a real chain, our panic handler writes the result
		assert.Equal(t, 0, writer.Code)
	})
}
