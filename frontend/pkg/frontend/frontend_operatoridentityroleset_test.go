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

package frontend

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	v20261001 "github.com/Azure/ARO-HCP/internal/azureapi/v20261001"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestArmResourceListOperatorIdentityRoleSets(t *testing.T) {
	tests := []struct {
		name                     string
		roleDefinitionConfigName azure.RoleDefinitionConfigSetName
	}{
		{
			name:                     "List operator identity role sets with dev config",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
		},
		{
			name:                     "List operator identity role sets with public config",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNamePublic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			// Return an empty iterator - this tests the endpoint works correctly
			// Testing deduplication and populated catalogs requires integration tests
			// with a real or fake Cluster Service HTTP endpoint
			mockCSClient.EXPECT().
				ListVersions().
				Return(&ocm.VersionsListIterator{}).
				Times(1)

			reg := prometheus.NewRegistry()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				newTestFrontendInformers(t, mockResourcesDBClient),
				mockCSClient,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
				azure.NewClusterScopedIdentitiesConfig(tt.roleDefinitionConfigName),
			)

			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			url := ts.URL + "/subscriptions/" + coreapitesting.TestSubscriptionID + "/providers/" + coreapi.ProviderNamespace +
				"/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperatorIdentityRoleSetResourceTypeName + "?api-version=" + coreapitesting.TestAPIVersion

			resp, err := ts.Client().Get(url)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var pagedResponse coreapi.PagedResponse
			err = json.Unmarshal(body, &pagedResponse)
			require.NoError(t, err)

			// With an empty iterator, we expect an empty list
			assert.Len(t, pagedResponse.Value, 0)
		})
	}
}

func TestGetOperatorIdentityRoleSet(t *testing.T) {
	tests := []struct {
		name                     string
		roleDefinitionConfigName azure.RoleDefinitionConfigSetName
		roleSetName              string
		setupMock                func(*ocm.MockClusterServiceClientSpec)
		expectedStatusCode       int
		expectRoles              bool
	}{
		{
			name:                     "Invalid version name returns 404",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
			roleSetName:              "not-a-version",
			setupMock: func(m *ocm.MockClusterServiceClientSpec) {
				// No mock setup needed - version parsing fails before Cluster Service call
			},
			expectedStatusCode: http.StatusNotFound,
			expectRoles:        false,
		},
		{
			name:                     "Valid version in catalog returns 200 with roles",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
			roleSetName:              "4.19",
			setupMock: func(m *ocm.MockClusterServiceClientSpec) {
				version, _ := arohcpv1alpha1.NewVersion().ID("openshift-v4.19.0").Build()
				m.EXPECT().
					ListVersions().
					Return(ocm.NewSimpleVersionsListIterator([]*arohcpv1alpha1.Version{version}, nil)).
					Times(1)
			},
			expectedStatusCode: http.StatusOK,
			expectRoles:        true,
		},
		{
			name:                     "Version not in catalog returns 404",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
			roleSetName:              "4.99",
			setupMock: func(m *ocm.MockClusterServiceClientSpec) {
				version, _ := arohcpv1alpha1.NewVersion().ID("openshift-v4.19.0").Build()
				m.EXPECT().
					ListVersions().
					Return(ocm.NewSimpleVersionsListIterator([]*arohcpv1alpha1.Version{version}, nil)).
					Times(1)
			},
			expectedStatusCode: http.StatusNotFound,
			expectRoles:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.setupMock(mockCSClient)

			reg := prometheus.NewRegistry()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				newTestFrontendInformers(t, mockResourcesDBClient),
				mockCSClient,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
				azure.NewClusterScopedIdentitiesConfig(tt.roleDefinitionConfigName),
			)

			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			url := ts.URL + "/subscriptions/" + coreapitesting.TestSubscriptionID + "/providers/" + coreapi.ProviderNamespace +
				"/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperatorIdentityRoleSetResourceTypeName + "/" + tt.roleSetName +
				"?api-version=" + coreapitesting.TestAPIVersion

			resp, err := ts.Client().Get(url)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedStatusCode, resp.StatusCode)

			if !tt.expectRoles {
				return
			}

			// For 200 responses, verify the role set structure
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var roleSet map[string]any
			err = json.Unmarshal(body, &roleSet)
			require.NoError(t, err)

			// Verify basic structure
			name, ok := roleSet["name"].(string)
			require.True(t, ok, "role set name should be a string")
			assert.Equal(t, tt.roleSetName, name)

			id, ok := roleSet["id"].(string)
			require.True(t, ok, "role set id should be a string")
			assert.Contains(t, id, tt.roleSetName, "ID should contain the version name")

			resourceType, ok := roleSet["type"].(string)
			require.True(t, ok, "role set type should be a string")
			assert.Equal(t, coreapi.OperatorIdentityRoleSetResourceType.String(), resourceType)

			// Verify properties structure
			properties, ok := roleSet["properties"].(map[string]any)
			require.True(t, ok, "properties should be a map")

			controlPlaneOps, ok := properties["controlPlaneOperators"].([]any)
			require.True(t, ok, "controlPlaneOperators should be an array")
			assert.NotEmpty(t, controlPlaneOps, "controlPlaneOperators should not be empty")

			dataPlaneOps, ok := properties["dataPlaneOperators"].([]any)
			require.True(t, ok, "dataPlaneOperators should be an array")
			assert.NotEmpty(t, dataPlaneOps, "dataPlaneOperators should not be empty")
		})
	}
}

// TestMarshalClusterScopedIdentitiesConfig tests the marshaling of ClusterScopedIdentitiesConfig
// into the ARM API format for operator identity role sets
func TestMarshalClusterScopedIdentitiesConfig(t *testing.T) {
	tests := []struct {
		name                     string
		version                  string
		roleDefinitionConfigName azure.RoleDefinitionConfigSetName
		expectControlPlaneOps    bool
		expectDataPlaneOps       bool
	}{
		{
			name:                     "Version 4.19 with dev config",
			version:                  "4.19.0",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
			expectControlPlaneOps:    true,
			expectDataPlaneOps:       true,
		},
		{
			name:                     "Version 4.19 with public config",
			version:                  "4.19.0",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNamePublic,
			expectControlPlaneOps:    true,
			expectDataPlaneOps:       true,
		},
		{
			name:                     "Version 4.20 with dev config",
			version:                  "4.20.0",
			roleDefinitionConfigName: azure.RoleDefinitionConfigSetNameDev,
			expectControlPlaneOps:    true,
			expectDataPlaneOps:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := semver.ParseTolerant(tt.version)
			require.NoError(t, err)

			resourceIDString := "/subscriptions/" + coreapitesting.TestSubscriptionID + "/providers/" + coreapi.ProviderNamespace +
				"/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperatorIdentityRoleSetResourceTypeName + "/" + tt.version
			resourceID, err := azcorearm.ParseResourceID(resourceIDString)
			require.NoError(t, err)

			config := azure.NewClusterScopedIdentitiesConfig(tt.roleDefinitionConfigName)
			apiRegistry := coreapi.NewAPIRegistry()

			// Register all API versions (normally done at startup)
			metadataapi.Must[any](nil, v20240610preview.RegisterVersion(apiRegistry))
			metadataapi.Must[any](nil, v20251223preview.RegisterVersion(apiRegistry))
			metadataapi.Must[any](nil, v20260630preview.RegisterVersion(apiRegistry))
			metadataapi.Must[any](nil, v20260901preview.RegisterVersion(apiRegistry))
			metadataapi.Must[any](nil, v20261001.RegisterVersion(apiRegistry))

			versionedInterface, ok := apiRegistry.Lookup(coreapitesting.TestAPIVersion)
			require.True(t, ok, "API version should be registered")

			data, err := marshalClusterScopedIdentitiesConfig(resourceID, &version, config, versionedInterface)
			require.NoError(t, err)

			var roleSet map[string]any
			err = json.Unmarshal(data, &roleSet)
			require.NoError(t, err)

			// Verify basic structure
			assert.Equal(t, resourceID.String(), roleSet["id"])
			assert.Equal(t, tt.version, roleSet["name"])
			assert.Equal(t, resourceID.ResourceType.String(), roleSet["type"])

			properties, ok := roleSet["properties"].(map[string]any)
			require.True(t, ok, "properties should be a map")

			// Verify controlPlaneOperators
			controlPlaneOps, ok := properties["controlPlaneOperators"].([]any)
			require.True(t, ok, "controlPlaneOperators should be an array")
			if tt.expectControlPlaneOps {
				assert.NotEmpty(t, controlPlaneOps, "controlPlaneOperators should not be empty")

				// Verify structure of first operator
				if len(controlPlaneOps) > 0 {
					op, ok := controlPlaneOps[0].(map[string]any)
					require.True(t, ok)
					assert.NotEmpty(t, op["name"], "operator name should not be empty")
					assert.Contains(t, []any{"Always", "OnEnablement"}, op["required"], "required should be Always or OnEnablement")

					roleDefinitions, ok := op["roleDefinitions"].([]any)
					require.True(t, ok, "roleDefinitions should be an array")
					assert.NotEmpty(t, roleDefinitions, "roleDefinitions should not be empty")

					// Verify first role definition structure
					if len(roleDefinitions) > 0 {
						rd, ok := roleDefinitions[0].(map[string]any)
						require.True(t, ok)
						assert.NotEmpty(t, rd["name"], "role definition name should not be empty")
						// resourceID might be "resourceId" in the JSON due to field name marshaling
						resourceID, hasResourceID := rd["resourceID"]
						resourceId, hasResourceId := rd["resourceId"]
						if hasResourceID {
							assert.NotEmpty(t, resourceID, "role definition resourceID should not be empty")
							assert.Contains(t, resourceID, "/providers/Microsoft.Authorization/roleDefinitions/")
						} else if hasResourceId {
							assert.NotEmpty(t, resourceId, "role definition resourceId should not be empty")
							assert.Contains(t, resourceId, "/providers/Microsoft.Authorization/roleDefinitions/")
						} else {
							t.Errorf("role definition should have either resourceID or resourceId field, got: %v", rd)
						}
					}
				}
			}

			// Verify dataPlaneOperators
			dataPlaneOps, ok := properties["dataPlaneOperators"].([]any)
			require.True(t, ok, "dataPlaneOperators should be an array")
			if tt.expectDataPlaneOps {
				assert.NotEmpty(t, dataPlaneOps, "dataPlaneOperators should not be empty")

				// Verify structure of first operator
				if len(dataPlaneOps) > 0 {
					op, ok := dataPlaneOps[0].(map[string]any)
					require.True(t, ok)
					assert.NotEmpty(t, op["name"], "operator name should not be empty")
					assert.Contains(t, []any{"Always", "OnEnablement"}, op["required"], "required should be Always or OnEnablement")

					roleDefinitions, ok := op["roleDefinitions"].([]any)
					require.True(t, ok, "roleDefinitions should be an array")
					assert.NotEmpty(t, roleDefinitions, "roleDefinitions should not be empty")
				}
			}
		})
	}
}
