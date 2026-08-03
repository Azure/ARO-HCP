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

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
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

func TestAllClustersMinimumVersionsHandler(t *testing.T) {
	tests := []struct {
		name               string
		body               string
		clusterCount       int
		expectedStatusCode int
		expectedError      string
		expectedCount      int
		expectedVersions   []string
	}{
		{
			name:               "invalid JSON body",
			body:               `{not json`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid JSON body",
		},
		{
			name:               "invalid semver",
			body:               `{"minimumVersions":["bad"]}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      `invalid semver "bad"`,
		},
		{
			name:               "updates multiple clusters",
			body:               `{"minimumVersions":["4.19.2","4.20.1"]}`,
			clusterCount:       3,
			expectedStatusCode: http.StatusOK,
			expectedCount:      3,
			expectedVersions:   []string{"4.19.2", "4.20.1"},
		},
		{
			name:               "empty list clears versions",
			body:               `{"minimumVersions":[]}`,
			clusterCount:       2,
			expectedStatusCode: http.StatusOK,
			expectedCount:      2,
		},
		{
			name:               "no clusters returns zero count",
			body:               `{"minimumVersions":["4.19.0"]}`,
			clusterCount:       0,
			expectedStatusCode: http.StatusOK,
			expectedCount:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			// Create SPCs for each cluster.
			clusterResourceIDs := make([]*azcorearm.ResourceID, 0, tt.clusterCount)
			for i := range tt.clusterCount {
				clusterRID, err := azcorearm.ParseResourceID(
					path.Join(
						"/subscriptions", api.TestSubscriptionID,
						"resourceGroups", api.TestResourceGroupName,
						"providers", api.ProviderNamespace, api.ClusterResourceTypeName,
						fmt.Sprintf("cluster%d", i),
					),
				)
				require.NoError(t, err)
				clusterResourceIDs = append(clusterResourceIDs, clusterRID)

				existing, err := database.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, clusterRID)
				require.NoError(t, err)
				// Pre-set some versions to confirm they get overwritten.
				existing.Spec.ControlPlaneVersion.MinimumVersions = []semver.Version{semver.MustParse("1.0.0")}
				_, err = mockResourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Replace(ctx, existing, nil)
				require.NoError(t, err)
			}

			handler := NewAllClustersMinimumVersionsHandler(mockResourcesDBClient)

			req := httptest.NewRequest(http.MethodPost, "/admin/v1/minimumversions", strings.NewReader(tt.body))
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err := handler.ServeHTTP(recorder, req)

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

			var respBody allClustersMinimumVersionsResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&respBody))
			if respBody.UpdatedCount != tt.expectedCount {
				t.Errorf("expected updatedCount %d, got %d", tt.expectedCount, respBody.UpdatedCount)
			}

			// Verify each SPC was updated.
			for _, clusterRID := range clusterResourceIDs {
				spc, err := mockResourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)

				if tt.expectedVersions == nil {
					if len(spc.Spec.ControlPlaneVersion.MinimumVersions) != 0 {
						t.Errorf("cluster %s: expected MinimumVersions cleared, got %v", clusterRID.Name, spc.Spec.ControlPlaneVersion.MinimumVersions)
					}
				} else {
					if len(spc.Spec.ControlPlaneVersion.MinimumVersions) != len(tt.expectedVersions) {
						t.Errorf("cluster %s: expected %d versions, got %d", clusterRID.Name, len(tt.expectedVersions), len(spc.Spec.ControlPlaneVersion.MinimumVersions))
					}
					for i, expected := range tt.expectedVersions {
						if i < len(spc.Spec.ControlPlaneVersion.MinimumVersions) && spc.Spec.ControlPlaneVersion.MinimumVersions[i].String() != expected {
							t.Errorf("cluster %s version[%d]: expected %q, got %q", clusterRID.Name, i, expected, spc.Spec.ControlPlaneVersion.MinimumVersions[i].String())
						}
					}
				}
			}
		})
	}
}
