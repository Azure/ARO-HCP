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

package hcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// mockFPACredentialRetriever implements fpa.FirstPartyApplicationTokenCredentialRetriever for testing
type mockFPACredentialRetriever struct {
	credential azcore.TokenCredential
	err        error
}

func (m *mockFPACredentialRetriever) RetrieveCredential(tenantId string, additionallyAllowedTenants ...string) (azcore.TokenCredential, error) {
	return m.credential, m.err
}

func TestSerialConsoleHandler(t *testing.T) {
	tests := []struct {
		name               string
		resourceID         string
		vmName             string
		setupData          func(context.Context, *testing.T, *databasetesting.MockResourcesDBClient, *azcorearm.ResourceID)
		mockFPA            *mockFPACredentialRetriever
		expectedStatusCode int
		expectedError      string
	}{
		{
			name:       "missing vmName parameter",
			resourceID: api.TestClusterResourceID,
			vmName:     "",
			setupData: func(ctx context.Context, t *testing.T, mockResourcesDBClient *databasetesting.MockResourcesDBClient, resourceID *azcorearm.ResourceID) {
			},
			mockFPA:            &mockFPACredentialRetriever{},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "vmName query parameter is required",
		},
		{
			name:       "invalid vmName format",
			resourceID: api.TestClusterResourceID,
			vmName:     "-invalid-vm-name",
			setupData: func(ctx context.Context, t *testing.T, mockResourcesDBClient *databasetesting.MockResourcesDBClient, resourceID *azcorearm.ResourceID) {
			},
			mockFPA:            &mockFPACredentialRetriever{},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "vmName contains invalid characters or format",
		},
		{
			name:       "HCP cluster not found in database",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupData: func(ctx context.Context, t *testing.T, mockResourcesDBClient *databasetesting.MockResourcesDBClient, resourceID *azcorearm.ResourceID) {
			},
			mockFPA:            &mockFPACredentialRetriever{},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get HCP from database",
		},
		{
			name:       "subscription not found",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupData: func(ctx context.Context, t *testing.T, mockResourcesDBClient *databasetesting.MockResourcesDBClient, resourceID *azcorearm.ResourceID) {
				// Create HCP cluster with InternalID
				internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-id")
				require.NoError(t, err)
				hcp := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{ID: resourceID},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: &internalID,
					},
				}
				_, err = mockResourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
				require.NoError(t, err)
			},
			mockFPA:            &mockFPACredentialRetriever{},
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:       "FPA credential retrieval fails",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupData: func(ctx context.Context, t *testing.T, mockResourcesDBClient *databasetesting.MockResourcesDBClient, resourceID *azcorearm.ResourceID) {
				// Create HCP cluster with InternalID
				internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-id")
				require.NoError(t, err)
				hcp := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{ID: resourceID},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: &internalID,
					},
				}
				_, err = mockResourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
				require.NoError(t, err)

				// Create subscription with tenant ID
				tenantID := "test-tenant-id"
				subscriptionResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + resourceID.SubscriptionID))
				subscription := &arm.Subscription{
					CosmosMetadata: arm.CosmosMetadata{
						ResourceID: subscriptionResourceID,
					},
					ResourceID: subscriptionResourceID,
					State:      arm.SubscriptionStateRegistered,
					Properties: &arm.SubscriptionProperties{
						TenantId: &tenantID,
					},
				}
				_, err = mockResourcesDBClient.Subscriptions().Create(ctx, subscription, nil)
				require.NoError(t, err)
			},
			mockFPA: &mockFPACredentialRetriever{
				err: fmt.Errorf("failed to get FPA credentials"),
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to retrieve Azure credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			// Setup database and test data
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			// Parse resource ID and add to context
			resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
			require.NoError(t, err)

			// Setup test data
			tt.setupData(ctx, t, mockResourcesDBClient, resourceID)

			// Create handler
			handler := NewHCPSerialConsoleHandler(mockResourcesDBClient, tt.mockFPA)

			ctx = utils.ContextWithResourceID(ctx, resourceID)

			// Create request
			url := "/serialconsole"
			if tt.vmName != "" {
				url += "?vmName=" + tt.vmName
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req = req.WithContext(ctx)

			// Execute request
			recorder := httptest.NewRecorder()
			err = handler.ServeHTTP(recorder, req)

			// Validate response
			if tt.expectedStatusCode >= 400 {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}

				// Check if it's a CloudError when status is 400 or 404
				if tt.expectedStatusCode == http.StatusBadRequest || tt.expectedStatusCode == http.StatusNotFound {
					var cloudErr *arm.CloudError
					if !errors.As(err, &cloudErr) {
						t.Errorf("Expected CloudError but got %T: %v", err, err)
						return
					}
					if cloudErr.StatusCode != tt.expectedStatusCode {
						t.Errorf("Expected status code %d but got %d", tt.expectedStatusCode, cloudErr.StatusCode)
					}
				}

				// Check error message
				if tt.expectedError != "" {
					if !strings.Contains(err.Error(), tt.expectedError) {
						t.Errorf("Expected error containing %q but got %q", tt.expectedError, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got %q", err.Error())
				}
			}
		})
	}
}

func TestSerialConsoleHandler_InvalidResourceID(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockFPA := &mockFPACredentialRetriever{}

	handler := NewHCPSerialConsoleHandler(mockResourcesDBClient, mockFPA)

	// Create request WITHOUT adding resource ID to context
	req := httptest.NewRequest(http.MethodGet, "/serialconsole?vmName=test-vm", nil)
	req = req.WithContext(ctx)

	recorder := httptest.NewRecorder()
	err := handler.ServeHTTP(recorder, req)

	if err == nil {
		t.Error("Expected error for missing resource ID but got none")
		return
	}

	// Should be a CloudError with 400 status
	var cloudErr *arm.CloudError
	if !errors.As(err, &cloudErr) {
		t.Errorf("Expected CloudError but got %T: %v", err, err)
		return
	}

	if cloudErr.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status code %d but got %d", http.StatusBadRequest, cloudErr.StatusCode)
	}

	if !strings.Contains(err.Error(), "invalid resource identifier") {
		t.Errorf("Expected error containing 'invalid resource identifier' but got %q", err.Error())
	}
}
