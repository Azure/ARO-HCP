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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	parsed, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return parsed
}

func mustNewInternalID(t *testing.T, path string) *api.InternalID {
	t.Helper()
	id, err := api.NewInternalID(path)
	require.NoError(t, err)
	return &id
}

func newManagementCluster(t *testing.T, stampIdentifier string) *fleet.ManagementCluster {
	t.Helper()
	managementClusterResourceID, err := fleet.ToManagementClusterResourceID(stampIdentifier)
	require.NoError(t, err)
	return &fleet.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   managementClusterResourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		ResourceID: managementClusterResourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks1"),
			PublicDNSZoneResourceID:                              mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"),
			ClusterServiceProvisionShardID:                       mustNewInternalID(t, "/api/aro_hcp/v1alpha1/provision_shards/00000000-0000-0000-0000-000000000001"),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-hc-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-hc-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "00000000-0000-0000-0000-000000000002",
			MaestroConsumerName:                                  "consumer1",
			MaestroRESTAPIURL:                                    "https://maestro.example.com",
			MaestroGRPCTarget:                                    "maestro.example.com:8090",
			KubeApplierCosmosContainerName:                       "kube-applier",
		},
	}
}

func TestManagementClusterGetHandler(t *testing.T) {
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
			name:                  "get existing management cluster",
			stampIdentifier:       "a1",
			managementClusterName: fleet.ManagementClusterResourceName,
			setupResources:        []any{newStamp("a1"), newManagementCluster(t, "a1")},
			expectedStatusCode:    http.StatusOK,
		},
		{
			name:                  "management cluster not found returns 404",
			stampIdentifier:       "a1",
			managementClusterName: fleet.ManagementClusterResourceName,
			setupResources:        []any{newStamp("a1")},
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "not found",
		},
		{
			name:                  "invalid stamp identifier returns 400",
			stampIdentifier:       "",
			managementClusterName: fleet.ManagementClusterResourceName,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "Invalid stamp identifier",
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

			handler := NewManagementClusterGetHandler(mockFleetDB)

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps/"+tt.stampIdentifier+"/managementClusters/"+tt.managementClusterName, nil)
			req.SetPathValue("stampIdentifier", tt.stampIdentifier)
			req.SetPathValue("managementClusterName", tt.managementClusterName)
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

				var resp ManagementCluster
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
				require.NotEmpty(t, resp.ResourceID)
				require.NotEmpty(t, resp.Status.AKSResourceID)
				require.NotEmpty(t, resp.Status.ClusterServiceProvisionShardID)
			}
		})
	}
}

func TestToManagementClusterStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      fleet.ManagementClusterStatus
		expectedErr string
	}{
		{
			name: "valid status converts successfully",
			status: fleet.ManagementClusterStatus{
				AKSResourceID:                  mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks1"),
				PublicDNSZoneResourceID:        mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"),
				ClusterServiceProvisionShardID: mustNewInternalID(t, "/api/aro_hcp/v1alpha1/provision_shards/00000000-0000-0000-0000-000000000001"),
				Conditions: []metav1.Condition{
					{
						Type:   string(fleet.ManagementClusterConditionReady),
						Status: metav1.ConditionTrue,
						Reason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
					},
				},
			},
		},
		{
			name: "nil aksResourceID returns error",
			status: fleet.ManagementClusterStatus{
				PublicDNSZoneResourceID:        mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"),
				ClusterServiceProvisionShardID: mustNewInternalID(t, "/api/aro_hcp/v1alpha1/provision_shards/00000000-0000-0000-0000-000000000001"),
			},
			expectedErr: "nil aksResourceID",
		},
		{
			name: "nil publicDNSZoneResourceID returns error",
			status: fleet.ManagementClusterStatus{
				AKSResourceID:                  mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks1"),
				ClusterServiceProvisionShardID: mustNewInternalID(t, "/api/aro_hcp/v1alpha1/provision_shards/00000000-0000-0000-0000-000000000001"),
			},
			expectedErr: "nil publicDNSZoneResourceID",
		},
		{
			name: "nil clusterServiceProvisionShardID returns error",
			status: fleet.ManagementClusterStatus{
				AKSResourceID:           mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks1"),
				PublicDNSZoneResourceID: mustParseResourceID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"),
			},
			expectedErr: "nil clusterServiceProvisionShardID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := toManagementClusterStatus(tt.status)
			if len(tt.expectedErr) > 0 {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.NotEmpty(t, result.AKSResourceID)
				require.NotEmpty(t, result.PublicDNSZoneResourceID)
				require.NotEmpty(t, result.ClusterServiceProvisionShardID)
				require.Len(t, result.Conditions, len(tt.status.Conditions))
			}
		})
	}
}
