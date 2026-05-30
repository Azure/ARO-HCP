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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestStampGetHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		stampIdentifier    string
		setupResources     []any
		expectedStatusCode int
		expectedError      string
	}{
		{
			name:               "get existing stamp",
			stampIdentifier:    "a1",
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "stamp not found returns 404",
			stampIdentifier:    "a1",
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "invalid stamp identifier returns 400",
			stampIdentifier:    "",
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "Invalid stamp identifier",
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

			handler := NewStampGetHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps/"+tt.stampIdentifier, nil)
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

				var resp Stamp
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
				require.NotEmpty(t, resp.ResourceID)
			}
		})
	}
}

func TestStampListHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		setupResources     []any
		expectedStatusCode int
		expectedCount      int
	}{
		{
			name:               "empty list returns empty array",
			expectedStatusCode: http.StatusOK,
			expectedCount:      0,
		},
		{
			name:               "list single stamp",
			setupResources:     []any{newStamp("a1")},
			expectedStatusCode: http.StatusOK,
			expectedCount:      1,
		},
		{
			name:               "list multiple stamps",
			setupResources:     []any{newStamp("a1"), newStamp("b2")},
			expectedStatusCode: http.StatusOK,
			expectedCount:      2,
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

			handler := NewStampListHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps", nil)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			handlerErr := handler.ServeHTTP(recorder, req)
			require.NoError(t, handlerErr)
			require.Equal(t, tt.expectedStatusCode, recorder.Code)

			var resp []Stamp
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
			require.Len(t, resp, tt.expectedCount)
		})
	}
}
