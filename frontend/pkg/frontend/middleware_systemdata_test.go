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
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareSystemData(t *testing.T) {
	const systemDataRaw = `
{
	"createdBy": "foo@bar.com",
	"createdByType": "Application",
	"createdAt": "2024-01-01T12:34:54.0000000Z",
	"lastModifiedBy": "00000000-0000-0000-0000-000000000000",
	"lastModifiedByType": "Application",
	"lastModifiedAt": "2024-01-01T12:34:54.0000000Z"
}`

	timestamp, err := time.Parse(time.RFC3339, "2024-01-01T12:34:54.0000000Z")
	require.NoError(t, err)

	tests := []struct {
		name               string
		systemData         string
		expectedSystemData *arm.SystemData
	}{
		{
			name:       "systemData provided",
			systemData: systemDataRaw,
			expectedSystemData: &arm.SystemData{
				CreatedBy:          "foo@bar.com",
				CreatedByType:      arm.CreatedByTypeApplication,
				CreatedAt:          &timestamp,
				LastModifiedByType: arm.CreatedByTypeApplication,
				LastModifiedBy:     "00000000-0000-0000-0000-000000000000",
				LastModifiedAt:     &timestamp,
			},
		},
		{
			name:               "systemData not provided",
			systemData:         "",
			expectedSystemData: nil,
		},
		{
			name:               "invalid",
			systemData:         "im_a_potato_not_a_json",
			expectedSystemData: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()

			request, err := http.NewRequest(http.MethodPut, "", bytes.NewReader([]byte("")))
			require.NoError(t, err)

			if tt.systemData != "" {
				request.Header = http.Header{
					arm.HeaderNameARMResourceSystemData: []string{tt.systemData},
				}
			}

			// Add a logger to the context so parsing errors will be logged.
			ctx := ContextWithLogger(request.Context(), slog.Default())
			request = request.WithContext(ctx)

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareSystemData(writer, request, next)

			result, err := SystemDataFromContext(request.Context())
			if err == nil {
				if !reflect.DeepEqual(result, tt.expectedSystemData) {
					t.Error(cmp.Diff(result, tt.expectedSystemData))
				}
			} else if tt.expectedSystemData != nil {
				t.Error("Expected SystemData in request context")
			}
		})
	}
}
