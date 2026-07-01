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

package hcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDesiredControlPlaneSizeHandler(t *testing.T) {
	smallStr := string(api.HostedClusterControlPlaneSizeSmall)
	xxlargeStr := string(api.HostedClusterControlPlaneSizeXXlarge)
	largeStr := string(api.HostedClusterControlPlaneSizeLarge)

	tests := []struct {
		name               string
		body               io.Reader
		skipResourceID     bool
		existingSize       *string
		expectedStatusCode int
		expectedError      string
		// expectSizeCleared asserts the stored size is nil after the request.
		expectSizeCleared bool
		// expectedSize asserts the stored size matches (only checked when
		// expectSizeCleared is false and the request succeeds).
		expectedSize *string
	}{
		{
			name:               "missing resource ID",
			body:               strings.NewReader(`{"size":"Small"}`),
			skipResourceID:     true,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid resource identifier in request",
		},
		{
			name:               "invalid JSON body",
			body:               strings.NewReader(`{not json`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid JSON body",
		},
		{
			name:               "empty string size rejected",
			body:               strings.NewReader(`{"size":""}`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "size must not be empty",
		},
		{
			name:               "invalid size value",
			body:               strings.NewReader(`{"size":"Tiny"}`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      `size "Tiny" must be one of Small, Medium, Large, Xlarge, XXlarge`,
		},
		{
			name:               "valid size Small creates SPC",
			body:               strings.NewReader(`{"size":"Small"}`),
			expectedStatusCode: http.StatusOK,
			expectedSize:       &smallStr,
		},
		{
			name:               "valid size XXlarge creates SPC",
			body:               strings.NewReader(`{"size":"XXlarge"}`),
			expectedStatusCode: http.StatusOK,
			expectedSize:       &xxlargeStr,
		},
		{
			name:               "overwrites existing size",
			body:               strings.NewReader(`{"size":"Small"}`),
			existingSize:       &largeStr,
			expectedStatusCode: http.StatusOK,
			expectedSize:       &smallStr,
		},
		{
			name:               "omitted size clears previously-set tier",
			body:               strings.NewReader(`{}`),
			existingSize:       &largeStr,
			expectedStatusCode: http.StatusOK,
			expectSizeCleared:  true,
		},
		{
			name:               "explicit null clears previously-set tier",
			body:               strings.NewReader(`{"size":null}`),
			existingSize:       &smallStr,
			expectedStatusCode: http.StatusOK,
			expectSizeCleared:  true,
		},
		{
			name:               "omitted size on fresh SPC is a no-op success",
			body:               strings.NewReader(`{}`),
			expectedStatusCode: http.StatusOK,
			expectSizeCleared:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
			require.NoError(t, err)

			if tt.existingSize != nil {
				existing, err := database.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
				require.NoError(t, err)
				existing.Spec.DesiredHostedClusterControlPlaneSize = tt.existingSize
				_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
				require.NoError(t, err)
			}

			handler := NewHCPDesiredControlPlaneSizeHandler(mockResourcesDBClient)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodPost, "/desiredcontrolplanesize", tt.body)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err = handler.ServeHTTP(recorder, req)

			if tt.expectedStatusCode >= 400 {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				var cloudErr *arm.CloudError
				if !errors.As(err, &cloudErr) {
					t.Fatalf("expected CloudError but got %T: %v", err, err)
				}
				if cloudErr.StatusCode != tt.expectedStatusCode {
					t.Errorf("expected status %d, got %d", tt.expectedStatusCode, cloudErr.StatusCode)
				}
				if tt.expectedError != "" && !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error but got %v", err)
			}

			// Verify the SPC was written with the expected size and nothing else changed.
			spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			var respBody desiredControlPlaneSizeRequest
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&respBody))

			if tt.expectSizeCleared {
				if spc.Spec.DesiredHostedClusterControlPlaneSize != nil {
					t.Errorf("expected DesiredHostedClusterControlPlaneSize cleared, got %q", *spc.Spec.DesiredHostedClusterControlPlaneSize)
				}
				if respBody.Size != nil {
					t.Errorf("expected response size nil, got %q", *respBody.Size)
				}
				return
			}

			if spc.Spec.DesiredHostedClusterControlPlaneSize == nil {
				t.Fatalf("expected DesiredHostedClusterControlPlaneSize to be set, got nil")
			}
			if *spc.Spec.DesiredHostedClusterControlPlaneSize != *tt.expectedSize {
				t.Errorf("expected size %q, got %q", *tt.expectedSize, *spc.Spec.DesiredHostedClusterControlPlaneSize)
			}
			if respBody.Size == nil || *respBody.Size != *tt.expectedSize {
				t.Errorf("expected response size %q, got %v", *tt.expectedSize, respBody.Size)
			}
		})
	}
}

func TestDesiredControlPlaneSizeHandler_PreservesOtherFields(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	// Seed an SPC with a populated Status to confirm the handler does not stomp it.
	existing, err := database.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
	require.NoError(t, err)
	mgmtResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	existing.Status.ManagementClusterResourceID = mgmtResourceID
	_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
	require.NoError(t, err)

	handler := NewHCPDesiredControlPlaneSizeHandler(mockResourcesDBClient)
	ctx = utils.ContextWithResourceID(ctx, resourceID)

	body := bytes.NewBufferString(`{"size":"Medium"}`)
	req := httptest.NewRequest(http.MethodPost, "/desiredcontrolplanesize", body)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))

	spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	if spc.Status.ManagementClusterResourceID == nil || spc.Status.ManagementClusterResourceID.String() != mgmtResourceID.String() {
		t.Errorf("expected ManagementClusterResourceID preserved, got %v", spc.Status.ManagementClusterResourceID)
	}
	if spc.Spec.DesiredHostedClusterControlPlaneSize == nil || *spc.Spec.DesiredHostedClusterControlPlaneSize != "Medium" {
		t.Errorf("expected size Medium, got %v", spc.Spec.DesiredHostedClusterControlPlaneSize)
	}
}
