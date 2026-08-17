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

func newManagementClusterScheduling(t *testing.T, stampIdentifier string) *fleetapi.ManagementClusterScheduling {
	t.Helper()
	schedulingResourceID, err := fleetapi.ToManagementClusterSchedulingResourceID(stampIdentifier)
	require.NoError(t, err)
	return &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   schedulingResourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		Status: fleetapi.ManagementClusterSchedulingStatus{
			Capacity: fleetapi.ManagementClusterCapacity{
				Current: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("32"),
				},
			},
			Scaling: fleetapi.ManagementClusterScaling{
				Max: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("64"),
				},
			},
		},
	}
}

func TestManagementClusterSchedulingGetHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		stampIdentifier       string
		managementClusterName string
		setupResources        []any
		expectedStatusCode    int
		expectedError         string
	}{
		{
			name:                  "get existing scheduling",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1"), newManagementClusterScheduling(t, "a1")},
			expectedStatusCode:    http.StatusOK,
		},
		{
			name:                  "scheduling not found returns 404",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "not found",
		},
		{
			name:                  "unknown management cluster returns 404",
			stampIdentifier:       "a1",
			managementClusterName: "nonexistent",
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "Management cluster",
		},
		{
			name:                  "invalid stamp identifier returns 400",
			stampIdentifier:       "",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "Invalid stamp identifier",
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

			handler := NewManagementClusterSchedulingGetHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps/"+tt.stampIdentifier+"/managementclusters/"+tt.managementClusterName+"/scheduling", nil)
			req.SetPathValue("stampIdentifier", tt.stampIdentifier)
			req.SetPathValue("managementClusterName", tt.managementClusterName)
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

				var resp fleetapi.ManagementClusterSchedulingStatus
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
				require.NotEmpty(t, resp.Capacity.Current)
				require.NotEmpty(t, resp.Scaling.Max)
			}
		})
	}
}
