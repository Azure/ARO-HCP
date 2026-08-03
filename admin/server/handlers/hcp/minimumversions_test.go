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

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestMinimumVersionsHandler(t *testing.T) {
	tests := []struct {
		name               string
		body               io.Reader
		skipResourceID     bool
		existingVersions   []semver.Version
		expectedStatusCode int
		expectedError      string
		expectCleared      bool
		expectedVersions   []string
	}{
		{
			name:               "missing resource ID",
			body:               strings.NewReader(`{"minimumVersions":["4.19.0"]}`),
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
			name:               "invalid semver",
			body:               strings.NewReader(`{"minimumVersions":["not-a-version"]}`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      `invalid semver "not-a-version"`,
		},
		{
			name:               "valid single version",
			body:               strings.NewReader(`{"minimumVersions":["4.19.2"]}`),
			expectedStatusCode: http.StatusOK,
			expectedVersions:   []string{"4.19.2"},
		},
		{
			name:               "valid multiple versions",
			body:               strings.NewReader(`{"minimumVersions":["4.19.2","4.20.1","5.0.0"]}`),
			expectedStatusCode: http.StatusOK,
			expectedVersions:   []string{"4.19.2", "4.20.1", "5.0.0"},
		},
		{
			name:               "empty list clears versions",
			body:               strings.NewReader(`{"minimumVersions":[]}`),
			existingVersions:   []semver.Version{semver.MustParse("4.19.0")},
			expectedStatusCode: http.StatusOK,
			expectCleared:      true,
		},
		{
			name:               "null clears versions",
			body:               strings.NewReader(`{"minimumVersions":null}`),
			existingVersions:   []semver.Version{semver.MustParse("4.19.0")},
			expectedStatusCode: http.StatusOK,
			expectCleared:      true,
		},
		{
			name:               "omitted field clears versions",
			body:               strings.NewReader(`{}`),
			existingVersions:   []semver.Version{semver.MustParse("4.19.0")},
			expectedStatusCode: http.StatusOK,
			expectCleared:      true,
		},
		{
			name:               "overwrites existing versions",
			body:               strings.NewReader(`{"minimumVersions":["4.20.0"]}`),
			existingVersions:   []semver.Version{semver.MustParse("4.19.0")},
			expectedStatusCode: http.StatusOK,
			expectedVersions:   []string{"4.20.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
			require.NoError(t, err)

			if tt.existingVersions != nil {
				existing, err := database.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
				require.NoError(t, err)
				existing.Spec.ControlPlaneVersion.MinimumVersions = tt.existingVersions
				_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
				require.NoError(t, err)
			}

			handler := NewHCPMinimumVersionsHandler(mockResourcesDBClient)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodPost, "/minimumversions", tt.body)
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

			spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			var respBody minimumVersionsResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&respBody))

			if tt.expectCleared {
				if len(spc.Spec.ControlPlaneVersion.MinimumVersions) != 0 {
					t.Errorf("expected MinimumVersions cleared, got %v", spc.Spec.ControlPlaneVersion.MinimumVersions)
				}
				if len(respBody.MinimumVersions) != 0 {
					t.Errorf("expected response minimumVersions empty/nil, got %v", respBody.MinimumVersions)
				}
				return
			}

			storedStrings := versionsToStrings(spc.Spec.ControlPlaneVersion.MinimumVersions)
			if len(storedStrings) != len(tt.expectedVersions) {
				t.Fatalf("expected %d versions stored, got %d: %v", len(tt.expectedVersions), len(storedStrings), storedStrings)
			}
			for i, expected := range tt.expectedVersions {
				if storedStrings[i] != expected {
					t.Errorf("stored version[%d]: expected %q, got %q", i, expected, storedStrings[i])
				}
			}
			if len(respBody.MinimumVersions) != len(tt.expectedVersions) {
				t.Fatalf("expected %d versions in response, got %d: %v", len(tt.expectedVersions), len(respBody.MinimumVersions), respBody.MinimumVersions)
			}
			for i, expected := range tt.expectedVersions {
				if respBody.MinimumVersions[i] != expected {
					t.Errorf("response version[%d]: expected %q, got %q", i, expected, respBody.MinimumVersions[i])
				}
			}
		})
	}
}

func TestMinimumVersionsHandler_PreservesOtherFields(t *testing.T) {
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

	handler := NewHCPMinimumVersionsHandler(mockResourcesDBClient)
	ctx = utils.ContextWithResourceID(ctx, resourceID)

	body := bytes.NewBufferString(`{"minimumVersions":["4.19.5"]}`)
	req := httptest.NewRequest(http.MethodPost, "/minimumversions", body)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))

	spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	if spc.Status.ManagementClusterResourceID == nil || spc.Status.ManagementClusterResourceID.String() != mgmtResourceID.String() {
		t.Errorf("expected ManagementClusterResourceID preserved, got %v", spc.Status.ManagementClusterResourceID)
	}
	if len(spc.Spec.ControlPlaneVersion.MinimumVersions) != 1 || spc.Spec.ControlPlaneVersion.MinimumVersions[0].String() != "4.19.5" {
		t.Errorf("expected MinimumVersions [4.19.5], got %v", spc.Spec.ControlPlaneVersion.MinimumVersions)
	}
}
