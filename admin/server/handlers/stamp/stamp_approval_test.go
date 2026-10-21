// Copyright 2026 Microsoft Corporation
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

package stamp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newStamp(stampIdentifier string) *fleet.Stamp {
	stampResourceID, _ := fleet.ToStampResourceID(stampIdentifier)
	return &fleet.Stamp{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: stampResourceID,
		},
		ResourceID: stampResourceID,
	}
}

func newStampWithConditions(stampIdentifier string, conditions ...metav1.Condition) *fleet.Stamp {
	stampResourceID, _ := fleet.ToStampResourceID(stampIdentifier)
	return &fleet.Stamp{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: stampResourceID,
		},
		ResourceID: stampResourceID,
		Status: fleet.StampStatus{
			Conditions: conditions,
		},
	}
}

func TestStampApprovalHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		stampIdentifier    string
		body               string
		setupResources     []any
		expectedStatusCode int
		expectedError      string
		verifyState        func(*testing.T, *databasetesting.MockFleetDBClient)
	}{
		{
			name:               "approve stamp",
			stampIdentifier:    "a1",
			body:               `{"approved":true,"reason":"ManuallyApproved","message":"Approved by SRE"}`,
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusNoContent,
			verifyState: func(t *testing.T, mock *databasetesting.MockFleetDBClient) {
				ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
				stamp, err := mock.Stamps().Get(ctx, "a1")
				require.NoError(t, err)
				cond := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleet.StampConditionApproved))
				require.NotNil(t, cond)
				require.Equal(t, metav1.ConditionTrue, cond.Status)
				require.Equal(t, "ManuallyApproved", cond.Reason)
				require.Equal(t, "Approved by SRE", cond.Message)
			},
		},
		{
			name:            "revoke approval",
			stampIdentifier: "a1",
			body:            `{"approved":false,"reason":"ApprovalRevoked","message":"Revoked for maintenance"}`,
			setupResources: []any{
				newStampWithConditions("a1", metav1.Condition{
					Type:   string(fleet.StampConditionApproved),
					Status: metav1.ConditionTrue,
					Reason: string(fleet.StampConditionReasonManuallyApproved),
				}),
			},
			expectedStatusCode: http.StatusNoContent,
			verifyState: func(t *testing.T, mock *databasetesting.MockFleetDBClient) {
				ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
				stamp, err := mock.Stamps().Get(ctx, "a1")
				require.NoError(t, err)
				cond := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleet.StampConditionApproved))
				require.NotNil(t, cond)
				require.Equal(t, metav1.ConditionFalse, cond.Status)
				require.Equal(t, "ApprovalRevoked", cond.Reason)
			},
		},
		{
			name:            "idempotent approval is no-op",
			stampIdentifier: "a1",
			body:            `{"approved":true,"reason":"ManuallyApproved","message":"Approved by SRE"}`,
			setupResources: []any{
				newStampWithConditions("a1", metav1.Condition{
					Type:   string(fleet.StampConditionApproved),
					Status: metav1.ConditionTrue,
					Reason: string(fleet.StampConditionReasonManuallyApproved),
				}),
			},
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:            "idempotent revocation is no-op",
			stampIdentifier: "a1",
			body:            `{"approved":false,"reason":"ApprovalRevoked","message":"Revoked"}`,
			setupResources: []any{
				newStampWithConditions("a1", metav1.Condition{
					Type:   string(fleet.StampConditionApproved),
					Status: metav1.ConditionFalse,
					Reason: string(fleet.StampConditionReasonApprovalRevoked),
				}),
			},
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:               "stamp not found returns 404",
			stampIdentifier:    "a1",
			body:               `{"approved":true,"reason":"ManuallyApproved","message":"Approved"}`,
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "missing reason returns 400",
			stampIdentifier:    "a1",
			body:               `{"approved":true,"reason":"","message":"Approved"}`,
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "reason is required",
		},
		{
			name:               "missing message returns 400",
			stampIdentifier:    "a1",
			body:               `{"approved":true,"reason":"ManuallyApproved","message":""}`,
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "message is required",
		},
		{
			name:               "invalid JSON body",
			stampIdentifier:    "a1",
			body:               `{invalid`,
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "could not be deserialized",
		},
		{
			name:               "missing both reason and message returns 400",
			stampIdentifier:    "a1",
			body:               `{"approved":true,"reason":"","message":""}`,
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "reason is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			var mockFleetDB *databasetesting.MockFleetDBClient
			var err error
			if len(tt.setupResources) > 0 {
				mockFleetDB, err = databasetesting.NewMockFleetDBClientWithResources(ctx, tt.setupResources)
				require.NoError(t, err)
			} else {
				mockFleetDB = databasetesting.NewMockFleetDBClient()
			}

			handler := NewStampApprovalHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodPost, "/admin/v1/stamps/"+tt.stampIdentifier+"/approval", strings.NewReader(tt.body))
			req.SetPathValue("stampIdentifier", tt.stampIdentifier)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			handlerErr := handler.ServeHTTP(recorder, req)

			if len(tt.expectedError) > 0 {
				require.Error(t, handlerErr)
				var cloudErr *arm.CloudError
				require.True(t, errors.As(handlerErr, &cloudErr), "expected CloudError but got %T: %v", handlerErr, handlerErr)
				require.Equal(t, tt.expectedStatusCode, cloudErr.StatusCode)
				require.Contains(t, cloudErr.Error(), tt.expectedError)
			} else {
				require.NoError(t, handlerErr)
				require.Equal(t, tt.expectedStatusCode, recorder.Code)
			}

			if tt.verifyState != nil {
				tt.verifyState(t, mockFleetDB)
			}
		})
	}
}
