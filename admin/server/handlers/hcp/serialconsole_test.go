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
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	sdk "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
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
		setupMocks         func(*gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever)
		expectedStatusCode int
		expectedError      string
	}{
		{
			name:       "missing vmName parameter",
			resourceID: api.TestClusterResourceID,
			vmName:     "",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				return database.NewMockDBClient(ctrl), ocm.NewMockClusterServiceClientSpec(ctrl), &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "vmName query parameter is required",
		},
		{
			name:       "invalid vmName format",
			resourceID: api.TestClusterResourceID,
			vmName:     "-invalid-vm-name",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				return database.NewMockDBClient(ctrl), ocm.NewMockClusterServiceClientSpec(ctrl), &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "vmName contains invalid characters or format",
		},
		{
			name:       "HCP cluster not found in database (generic error)",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				mockDB := database.NewMockDBClient(ctrl)
				mockCRUD := database.NewMockHCPClusterCRUD(ctrl)

				resourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
				mockDB.EXPECT().
					HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
					Return(mockCRUD)
				mockCRUD.EXPECT().
					Get(gomock.Any(), resourceID.Name).
					Return(nil, fmt.Errorf("failed to get HCP from database: cluster not found"))

				return mockDB, ocm.NewMockClusterServiceClientSpec(ctrl), &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get HCP from database",
		},
		{
			name:       "HCP cluster not found in database (404 ResponseError)",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				mockDB := database.NewMockDBClient(ctrl)
				mockCRUD := database.NewMockHCPClusterCRUD(ctrl)

				resourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
				mockDB.EXPECT().
					HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
					Return(mockCRUD)
				mockCRUD.EXPECT().
					Get(gomock.Any(), resourceID.Name).
					Return(nil, &azcore.ResponseError{StatusCode: http.StatusNotFound})

				return mockDB, ocm.NewMockClusterServiceClientSpec(ctrl), &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get HCP from database", // Wrapped error, ReportError converts to 404 ResourceNotFoundError
		},
		{
			name:       "CS cluster not found (OCM 404)",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				mockDB := database.NewMockDBClient(ctrl)
				mockCRUD := database.NewMockHCPClusterCRUD(ctrl)

				resourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
				clusterServiceID, _ := api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cs-id")

				hcp := &api.HCPOpenShiftCluster{
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: clusterServiceID,
					},
				}

				mockDB.EXPECT().
					HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
					Return(mockCRUD)
				mockCRUD.EXPECT().
					Get(gomock.Any(), resourceID.Name).
					Return(hcp, nil)

				// Create OCM 404 error
				ocmErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()

				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					GetCluster(gomock.Any(), clusterServiceID).
					Return(nil, ocmErr)

				return mockDB, mockCS, &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "cluster data not found in cluster service",
		},
		{
			name:       "CS cluster retrieval fails (generic error)",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				mockDB := database.NewMockDBClient(ctrl)
				mockCRUD := database.NewMockHCPClusterCRUD(ctrl)

				resourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
				clusterServiceID, _ := api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cs-id")

				hcp := &api.HCPOpenShiftCluster{
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: clusterServiceID,
					},
				}

				mockDB.EXPECT().
					HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
					Return(mockCRUD)
				mockCRUD.EXPECT().
					Get(gomock.Any(), resourceID.Name).
					Return(hcp, nil)

				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					GetCluster(gomock.Any(), clusterServiceID).
					Return(nil, fmt.Errorf("CS cluster not found"))

				return mockDB, mockCS, &mockFPACredentialRetriever{}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get cluster data from cluster service",
		},
		{
			name:       "FPA credential retrieval fails",
			resourceID: api.TestClusterResourceID,
			vmName:     "test-vm",
			setupMocks: func(ctrl *gomock.Controller) (database.DBClient, ocm.ClusterServiceClientSpec, *mockFPACredentialRetriever) {
				mockDB := database.NewMockDBClient(ctrl)
				mockCRUD := database.NewMockHCPClusterCRUD(ctrl)

				resourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
				clusterServiceID, _ := api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cs-id")

				hcp := &api.HCPOpenShiftCluster{
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: clusterServiceID,
					},
				}

				mockDB.EXPECT().
					HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
					Return(mockCRUD)
				mockCRUD.EXPECT().
					Get(gomock.Any(), resourceID.Name).
					Return(hcp, nil)

				// Mock CS cluster with Azure tenant ID
				csCluster, _ := sdk.NewCluster().
					Azure(sdk.NewAzure().TenantID("test-tenant-id")).
					Build()

				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					GetCluster(gomock.Any(), clusterServiceID).
					Return(csCluster, nil)

				// Mock FPA failure
				mockFPA := &mockFPACredentialRetriever{
					err: fmt.Errorf("failed to get FPA credentials"),
				}

				return mockDB, mockCS, mockFPA
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to retrieve Azure credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mocks
			mockDB, mockCS, mockFPA := tt.setupMocks(ctrl)

			// Create handler
			handler := NewHCPSerialConsoleHandler(mockDB, mockCS, mockFPA)

			// Parse resource ID and add to context
			resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
			if err != nil {
				t.Fatalf("Failed to parse resource ID: %v", err)
			}
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDB := database.NewMockDBClient(ctrl)
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockFPA := &mockFPACredentialRetriever{}

	handler := NewHCPSerialConsoleHandler(mockDB, mockCS, mockFPA)

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
