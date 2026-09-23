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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestManagementClusterSchedulingPolicyPutHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		stampIdentifier        string
		managementClusterName  string
		requestBody            string
		setupResources         []any
		expectedStatusCode     int
		expectedError          string
		expectedSchedulePolicy fleetapi.ManagementClusterSchedulingPolicy
	}{
		{
			name:                   "update scheduling policy from schedulable to unschedulable",
			stampIdentifier:        "a1",
			managementClusterName:  fleetapi.ManagementClusterResourceName,
			requestBody:            `{"schedulingPolicy": "Unschedulable"}`,
			setupResources:         []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:     http.StatusOK,
			expectedSchedulePolicy: fleetapi.ManagementClusterSchedulingPolicyUnschedulable,
		},
		{
			name:                   "update scheduling policy from unschedulable to schedulable",
			stampIdentifier:        "a1",
			managementClusterName:  fleetapi.ManagementClusterResourceName,
			requestBody:            `{"schedulingPolicy": "Schedulable"}`,
			setupResources:         []any{newStamp("a1"), newManagementClusterWithPolicy(t, "a1", fleetapi.ManagementClusterSchedulingPolicyUnschedulable)},
			expectedStatusCode:     http.StatusOK,
			expectedSchedulePolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
		},
		{
			name:                  "invalid scheduling policy returns 400",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			requestBody:           `{"schedulingPolicy": "Invalid"}`,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "schedulingPolicy",
		},
		{
			name:                  "empty scheduling policy returns 400",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			requestBody:           `{"schedulingPolicy": ""}`,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "schedulingPolicy",
		},
		{
			name:                  "invalid JSON body returns 400",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			requestBody:           `not json`,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "invalid JSON body",
		},
		{
			name:                  "management cluster not found returns 404",
			stampIdentifier:       "a1",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			requestBody:           `{"schedulingPolicy": "Schedulable"}`,
			setupResources:        []any{newStamp("a1")},
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "not found",
		},
		{
			name:                  "invalid stamp identifier returns 400",
			stampIdentifier:       "",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			requestBody:           `{"schedulingPolicy": "Schedulable"}`,
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

			handler := NewManagementClusterSchedulingPolicyPutHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodPut, "/admin/v1/stamps/"+tt.stampIdentifier+"/managementclusters/"+tt.managementClusterName+"/schedulingpolicy", strings.NewReader(tt.requestBody))
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

				var resp schedulingPolicyRequest
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
				require.Equal(t, tt.expectedSchedulePolicy, resp.SchedulingPolicy)

				// Verify the policy was persisted
				managementCluster, err := mockFleetDB.Stamps().ManagementClusters(tt.stampIdentifier).Get(ctx, tt.managementClusterName)
				require.NoError(t, err)
				require.Equal(t, tt.expectedSchedulePolicy, managementCluster.Spec.SchedulingPolicy)
			}
		})
	}
}
