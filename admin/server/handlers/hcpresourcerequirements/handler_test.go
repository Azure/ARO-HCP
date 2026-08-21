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

package hcpresourcerequirements

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newHCPResourceRequirements(t *testing.T, name string) *fleetapi.HCPResourceRequirements {
	t.Helper()
	resourceID, err := fleetapi.ToHCPResourceRequirementsResourceID(name)
	require.NoError(t, err)
	return &fleetapi.HCPResourceRequirements{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(fleetapi.HCPResourceRequirementsResourceTypeName),
		},
		Status: fleetapi.HCPResourceRequirementsStatus{
			AverageRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
			SampleSize: 10,
		},
	}
}

func TestHCPResourceRequirementsGetHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		resourceName       string
		setupResources     []any
		expectedStatusCode int
		expectedError      string
	}{
		{
			name:               "get existing resource requirements",
			resourceName:       fleetapi.HCPResourceRequirementsResourceName,
			setupResources:     []any{newHCPResourceRequirements(t, fleetapi.HCPResourceRequirementsResourceName)},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "not found returns 404",
			resourceName:       "nonexistent",
			setupResources:     nil,
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			var mockFleetDB *fleetcosmosstoragetesting.MockFleetDBClient
			var err error
			if len(tt.setupResources) > 0 {
				mockFleetDB, err = fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, tt.setupResources)
				require.NoError(t, err)
			} else {
				mockFleetDB = fleetcosmosstoragetesting.NewMockFleetDBClient()
			}

			handler := NewHCPResourceRequirementsGetHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/hcpresourcerequirements/"+tt.resourceName, nil)
			req.SetPathValue("name", tt.resourceName)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			handlerErr := handler.ServeHTTP(recorder, req)

			if len(tt.expectedError) > 0 {
				require.Error(t, handlerErr)
				var cloudErr *coreapi.CloudError
				require.True(t, errors.As(handlerErr, &cloudErr), "expected CloudError but got %T: %v", handlerErr, handlerErr)
				require.Equal(t, tt.expectedStatusCode, cloudErr.StatusCode)
				require.Contains(t, cloudErr.Error(), tt.expectedError)
			} else {
				require.NoError(t, handlerErr)
				require.Equal(t, tt.expectedStatusCode, recorder.Code)

				var resp fleetapi.HCPResourceRequirementsStatus
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
				require.NotEmpty(t, resp.AverageRequests)
				require.Equal(t, 10, resp.SampleSize)
			}
		})
	}
}
