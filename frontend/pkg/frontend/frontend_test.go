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

package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newClusterResourceID(t *testing.T) *azcorearm.ResourceID {
	resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)
	require.NoError(t, err)
	return resourceID
}

func newClusterInternalID(t *testing.T) ocm.InternalID {
	internalID, err := metadataapi.NewInternalID(ocm.GenerateOCMCommercialClusterHREF("myCluster"))
	require.NoError(t, err)
	return internalID
}

// newTestSubscription creates a properly-formed subscription with CosmosMetadata set
func newTestSubscription(subscriptionID string, state coreapi.SubscriptionState, props *coreapi.SubscriptionProperties) *coreapi.Subscription {
	resourceID := metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(subscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		State:            state,
		RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
		Properties:       props,
	}
}

func TestOperationsList(t *testing.T) {
	// Required operations for all resource providers.
	// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/proxy-api-reference.md#required-operations
	requiredOperations := sets.New[string](
		path.Join(coreapi.ProviderNamespace, "register", coreapi.NamespaceOperationAction),
		path.Join(coreapi.ProviderNamespace, "unregister", coreapi.NamespaceOperationAction), // undocumented
	)

	reg := prometheus.NewRegistry()
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	f := NewFrontend(
		testr.New(t),
		nil,
		nil,
		reg,
		reg,
		mockResourcesDBClient,
		nil,
		newNoopAuditClient(t),
		coreapitesting.TestLocation,
		true,
	)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ts := newHTTPServer(ctx, f, nil, nil)

	// Use a bogus API version. Frontend should disregard it.
	resp, err := ts.Client().Get(ts.URL + "/providers/" + coreapi.ProviderNamespace + "/operations?api-version=1999-12-31")
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var pagedResponse coreapi.PagedResponse
	err = json.Unmarshal(body, &pagedResponse)
	require.NoError(t, err)

	assert.NotEmpty(t, pagedResponse.Value)
	for _, rawMessage := range pagedResponse.Value {
		var operation map[string]any

		err = json.Unmarshal(rawMessage, &operation)
		require.NoError(t, err)

		// Verify required fields are present and not empty.
		if assert.Contains(t, operation, "name") {
			name := operation["name"].(string)
			delete(requiredOperations, name)

			// Validate operation name.
			nameSegments := strings.Split(name, "/")
			assert.NotEmpty(t, nameSegments)
			assert.Equal(t, coreapi.ProviderNamespace, nameSegments[0])
			assert.Contains(t, coreapi.ValidNamespaceOperations, nameSegments[len(nameSegments)-1])
		}
		if assert.Contains(t, operation, "display") {
			display := operation["display"].(map[string]any)
			if assert.Contains(t, display, "provider") {
				assert.Equal(t, ProviderDisplay, display["provider"].(string))
			}
			if assert.Contains(t, display, "resource") {
				assert.NotEmpty(t, display["resource"].(string))
			}
			if assert.Contains(t, display, "operation") {
				assert.NotEmpty(t, display["operation"].(string))
			}
			// XXX Disabled while we host ARO Classic operations.
			//if assert.Contains(t, display, "description") {
			//	assert.NotEmpty(t, display["description"].(string))
			//}
		}
		if assert.Contains(t, operation, "isDataAction") {
			// All ARO-HCP operations are for ARM/control-plane.
			assert.Equal(t, false, operation["isDataAction"].(bool))
		}

		// Origin field is optional; default is "user,system".
		if origin, ok := operation["origin"]; ok {
			originStr, ok := origin.(string)
			require.True(t, ok)
			assert.Contains(t, coreapi.ValidNamespaceOperationOrigins, coreapi.NamespaceOperationOrigin(originStr))
		}
	}

	assert.Empty(t, requiredOperations)
}

func TestSubscriptionsGET(t *testing.T) {
	tests := []struct {
		name               string
		subDoc             *coreapi.Subscription
		expectedStatusCode int
	}{
		{
			name:               "GET Subscription - Doc Exists",
			subDoc:             newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "GET Subscription - No Doc",
			subDoc:             nil,
			expectedStatusCode: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			// Pre-populate subscription in the mock database
			subs := make(map[string]*coreapi.Subscription)
			if test.subDoc != nil {
				subs[coreapitesting.TestSubscriptionID] = test.subDoc
			}
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			rs, err := ts.Client().Get(ts.URL + coreapitesting.TestSubscriptionResourceID + "?api-version=" + coreapi.SubscriptionAPIVersion)
			require.NoError(t, err)

			assert.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)
			assertHTTPMetrics(t, reg)
		})
	}
}

func TestSubscriptionsPUT(t *testing.T) {
	tests := []struct {
		name               string
		urlPath            string
		subscription       *coreapi.Subscription
		subDoc             *coreapi.Subscription
		expectUpdated      bool
		expectedStatusCode int
	}{
		{
			name:    "PUT Subscription - Doc does not exist",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				CosmosMetadata:   coreapi.CosmosMetadata{ResourceID: metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(coreapitesting.TestSubscriptionID))},
				State:            coreapi.SubscriptionStateRegistered,
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties: &coreapi.SubscriptionProperties{
					TenantId: metadataapihelpers.Ptr("12345678-1234-1234-1234-123456789abc"),
					AdditionalProperties: &map[string]any{
						"foo": "bar",
						"baz": []int{1, 2, 3, 4},
						"test": struct{ blah string }{
							"hello",
						},
					},
					ManagedByTenants: &[]map[string]string{
						{
							"tenantId": "12345678-1234-1234-1234-123456789abc",
						},
					},
				},
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Update with no changes",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				CosmosMetadata:   coreapi.CosmosMetadata{ResourceID: metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(coreapitesting.TestSubscriptionID))},
				State:            coreapi.SubscriptionStateRegistered,
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			expectUpdated:      false,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Update registered features",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				CosmosMetadata:   coreapi.CosmosMetadata{ResourceID: metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(coreapitesting.TestSubscriptionID))},
				State:            coreapi.SubscriptionStateRegistered,
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties: &coreapi.SubscriptionProperties{
					RegisteredFeatures: &[]coreapi.Feature{
						{
							Name:  metadataapihelpers.Ptr("Microsoft.RedHatOpenShift/TestFeature"),
							State: metadataapihelpers.Ptr("Registered"),
						},
					},
				},
			},
			subDoc:             newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			expectUpdated:      true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Invalid Subscription",
			urlPath: "/subscriptions/oopsie-i-no-good0",
			subscription: &coreapi.Subscription{
				State:            coreapi.SubscriptionStateRegistered,
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing State",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Invalid State",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				State:            "Bogus",
				RegistrationDate: metadataapihelpers.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing RegistrationDate",
			urlPath: coreapitesting.TestSubscriptionResourceID,
			subscription: &coreapi.Subscription{
				State:      coreapi.SubscriptionStateRegistered,
				Properties: nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			body, err := json.Marshal(&test.subscription)
			require.NoError(t, err)

			subs := make(map[string]*coreapi.Subscription)
			if test.subDoc != nil {
				subs[coreapitesting.TestSubscriptionID] = test.subDoc
			}
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			urlPath := test.urlPath + "?api-version=" + coreapi.SubscriptionAPIVersion
			req, err := http.NewRequest(http.MethodPut, ts.URL+urlPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			rs, err := ts.Client().Do(req)
			require.NoError(t, err)

			assert.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)
			if test.expectedStatusCode != http.StatusBadRequest {
				assertHTTPMetrics(t, reg)
			}
		})
	}
}

type expectedPreflightError struct {
	message string // Expected error message (partial match)
	target  string // Expected target field path
}

func TestDeploymentPreflight(t *testing.T) {
	tests := []struct {
		name         string
		resource     map[string]any
		expectStatus coreapi.DeploymentPreflightStatus
		expectErrors []expectedPreflightError
	}{
		{
			name: "Unhandled resource type returns no error",
			resource: map[string]any{
				"name":       "virtual-machine",
				"type":       "Microsoft.Compute/virtualMachines",
				"location":   "eastus",
				"apiVersion": "2024-07-01",
			},
			expectStatus: coreapi.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Unrecognized API version returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       coreapi.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": "1980-01-01",
			},
			expectStatus: coreapi.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Well-formed cluster resource returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       coreapi.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": coreapitesting.TestAPIVersion,
				"systemData": map[string]any{
					"createdBy":     "test-user",
					"createdByType": "User",
					"createdAt":     "2025-01-01T00:00:00Z",
				},
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "4.20",
						"channelGroup": "stable",
					},
					"api": map[string]any{
						"visibility": "Public",
					},
					"platform": map[string]any{
						"subnetId":               coreapitesting.TestSubnetResourceID,
						"networkSecurityGroupId": coreapitesting.TestNetworkSecurityGroupResourceID,
					},
					"etcd": map[string]any{
						"dataEncryption": map[string]any{
							"keyManagementMode": "CustomerManaged",
							"customerManaged": map[string]any{
								"encryptionType": "KMS",
								"kms": map[string]any{
									"visibility": "Public",
									"activeKey": map[string]any{
										"name":      "test-key",
										"vaultName": "test-vault",
										"version":   "test-version",
									},
								},
							},
						},
					},
				},
			},
			expectStatus: coreapi.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches cluster resource with invalid fields",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       coreapi.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": coreapitesting.TestAPIVersion,
				"systemData": map[string]any{
					"createdBy":     "test-user",
					"createdByType": "User",
					"createdAt":     "2025-01-01T00:00:00Z",
				},
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"network": map[string]any{
						// 1 invalid fields
						"podCidr": "invalidCidr",
					},
					"api": map[string]any{
						// 1 invalid field
						"visibility": "invisible",
					},
					"platform": map[string]any{
						// 2 missing required fields
					},
				},
			},
			expectStatus: coreapi.DeploymentPreflightStatusFailed,
			expectErrors: []expectedPreflightError{
				{message: "Required value", target: "properties.version.id"},
				{message: "Invalid value: \"invalidCidr\": invalid CIDR address: invalidCidr", target: "properties.network.podCidr"},
				{message: "Unsupported value: \"invisible\": supported values: \"Private\", \"Public\"", target: "properties.api.visibility"},
				{message: "Required value", target: "properties.platform.subnetId"},
				{message: "Required value", target: "properties.platform.networkSecurityGroupId"},
				{message: "Unsupported value: \"PlatformManaged\": supported values: \"CustomerManaged\"", target: "properties.etcd.dataEncryption.keyManagementMode"},
				{message: "Required value", target: "properties.platform.subnetId"},
				{message: "Required value", target: "properties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "Well-formed node pool resource returns no error",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       coreapi.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": coreapitesting.TestAPIVersion,
				"systemData": map[string]any{
					"createdBy":     "test-user",
					"createdByType": "User",
					"createdAt":     "2025-01-01T00:00:00Z",
				},
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "4.20.16",
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						"vmSize": "Standard_D8s_v3",
					},
				},
			},
			expectStatus: coreapi.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches node pool resource with invalid fields",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       coreapi.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": coreapitesting.TestAPIVersion,
				"systemData": map[string]any{
					"createdBy":     "test-user",
					"createdByType": "User",
					"createdAt":     "2025-01-01T00:00:00Z",
				},
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "4.20.16",
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						"vmSize": "Standard_D8s_v3",
						"osDisk": map[string]any{
							// 1 invalid field
							"sizeGiB": -1,
						},
					},
					"autoScaling": map[string]any{
						// 1 invalid field
						"min": 3,
						"max": 1,
					},
					"taints": []map[string]any{
						{
							// 1 invalid + 1 missing required fields
							"effect": "NoTouchy",
						},
					},
				},
			},
			expectStatus: coreapi.DeploymentPreflightStatusFailed,
			expectErrors: []expectedPreflightError{
				{message: "Invalid value: -1: must be greater than or equal to 64", target: "properties.platform.osDisk.sizeGiB"},
				{message: "Invalid value: 1: must be greater than or equal to 3", target: "properties.autoScaling.max"},
				{message: "Unsupported value: \"NoTouchy\": supported values: \"NoExecute\", \"NoSchedule\", \"PreferNoSchedule\"", target: "properties.taints[0].effect"},
				{message: "Required value", target: "properties.taints[0].key"},
				{message: "Invalid value: \"\": name part must be non-empty", target: "properties.taints[0].key"},
				{message: "Invalid value: \"\": name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')", target: "properties.taints[0].key"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preflightPath := path.Join(coreapitesting.TestDeploymentResourceID, "preflight")

			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			resource, err := json.Marshal(&test.resource)
			require.NoError(t, err)
			preflightReq := coreapi.DeploymentPreflight{
				Resources: []json.RawMessage{resource},
			}
			body, err := json.Marshal(&preflightReq)
			require.NoError(t, err)

			req, err := http.NewRequest(http.MethodPost, ts.URL+preflightPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := ts.Client().Do(req)
			require.NoError(t, err)

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			var preflightResp coreapi.DeploymentPreflightResponse
			err = json.Unmarshal(body, &preflightResp)
			require.NoError(t, err)

			assert.Equal(t, test.expectStatus, preflightResp.Status)
			if len(test.expectErrors) == 0 {
				assert.Nil(t, preflightResp.Error)
			} else {
				if assert.NotNil(t, preflightResp.Error, "Expected validation errors but got none") {
					if len(test.expectErrors) == 1 {
						// Single error case - check main error fields
						assert.Nil(t, preflightResp.Error.Details)
						assert.NotEmpty(t, preflightResp.Error.Code)
						assert.NotEmpty(t, preflightResp.Error.Message)
						assert.NotEmpty(t, preflightResp.Error.Target)
						// Check the expected error details
						expected := test.expectErrors[0]
						assert.Equal(t, expected.message, preflightResp.Error.Message)
						assert.Equal(t, expected.target, preflightResp.Error.Target)
					} else {
						// Multiple errors case - check error details
						if !assert.Equal(t, len(test.expectErrors), len(preflightResp.Error.Details), "Number of validation errors mismatch") {
							// Print all actual errors when counts don't match
							t.Logf("Expected %d errors, got %d errors", len(test.expectErrors), len(preflightResp.Error.Details))
							t.Logf("Actual errors:")
							for i, actual := range preflightResp.Error.Details {
								t.Logf("  Error %d: Target=%s, Message=%s", i+1, actual.Target, actual.Message)
							}
						} else {
							for i, expected := range test.expectErrors {
								if i < len(preflightResp.Error.Details) {
									actual := preflightResp.Error.Details[i]
									assert.Equal(t, expected.message, actual.Message, "Error %d message mismatch", i+1)
									assert.Equal(t, expected.target, actual.Target, "Error %d target mismatch", i+1)
								}
							}
						}
					}
				}
			}
		})
	}
}

func TestRequestAdminCredential(t *testing.T) {
	type testCase struct {
		name                         string
		clusterProvisioningState     coreapi.ProvisioningState
		revokeCredentialsOperationID string
		statusCode                   int
	}

	tests := []testCase{
		{
			name:                         "Request conflict: credentials revoking",
			clusterProvisioningState:     coreapi.ProvisioningStateSucceeded,
			revokeCredentialsOperationID: "revocation-in-progress",
			statusCode:                   http.StatusConflict,
		},
	}

	for clusterProvisioningState := range coreapihelpers.ListProvisioningStates() {
		test := testCase{
			clusterProvisioningState: clusterProvisioningState,
		}
		if clusterProvisioningState.IsTerminal() {
			test.name = "Request accepted: cluster state=" + string(clusterProvisioningState)
			test.statusCode = http.StatusAccepted
		} else {
			test.name = "Request conflict: cluster state=" + string(clusterProvisioningState)
			test.statusCode = http.StatusConflict
		}
		tests = append(tests, test)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clusterResourceID := newClusterResourceID(t)
			clusterInternalID := newClusterInternalID(t)

			requestPath := path.Join(clusterResourceID.String(), "requestAdminCredential")

			reg := prometheus.NewRegistry()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			// Pre-populate the mock database with cluster and subscription
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

			cluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   clusterResourceID,
					PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{
						ID: clusterResourceID,
					},
				},
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					ProvisioningState:            test.clusterProvisioningState,
					ClusterServiceID:             &clusterInternalID,
					RevokeCredentialsOperationID: test.revokeCredentialsOperationID,
				},
			}
			_, err := mockResourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Create(ctx, cluster, nil)
			require.NoError(t, err)

			// Add active revoke operation if needed
			if test.clusterProvisioningState.IsTerminal() && len(test.revokeCredentialsOperationID) > 0 {
				operationID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperationStatusResourceTypeName + "/" + uuid.New().String()))
				resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/hcpOperationStatuses/" + uuid.New().String()))
				revokeOp := &coreapi.Operation{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   resourceID,
						PartitionKey: strings.ToLower(resourceID.SubscriptionID),
					},
					OperationID: operationID,
					Request:     cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation,
					ExternalID:  clusterResourceID,
					InternalID:  clusterInternalID,
					Status:      coreapi.ProvisioningStateDeleting,
				}
				_, err := mockResourcesDBClient.Operations(clusterResourceID.SubscriptionID).Create(ctx, revokeOp, nil)
				require.NoError(t, err)
			}

			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			url := ts.URL + requestPath + "?api-version=" + coreapitesting.TestAPIVersion
			resp, err := ts.Client().Post(url, "", nil)
			require.NoError(t, err)

			if !assert.Equal(t, test.statusCode, resp.StatusCode) {
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				fmt.Println(string(body))
			}
		})
	}
}

func TestRevokeCredentials(t *testing.T) {
	type testCase struct {
		name                         string
		clusterProvisioningState     coreapi.ProvisioningState
		revokeCredentialsOperationID string
		statusCode                   int
	}

	tests := []testCase{
		{
			name:                         "Request conflict: credentials revoking",
			clusterProvisioningState:     coreapi.ProvisioningStateSucceeded,
			revokeCredentialsOperationID: "revocation-in-progress",
			statusCode:                   http.StatusConflict,
		},
	}

	for clusterProvisioningState := range coreapihelpers.ListProvisioningStates() {
		test := testCase{
			clusterProvisioningState: clusterProvisioningState,
		}
		if clusterProvisioningState.IsTerminal() {
			test.name = "Request accepted: cluster state=" + string(clusterProvisioningState)
			test.statusCode = http.StatusAccepted
		} else {
			test.name = "Request conflict: cluster state=" + string(clusterProvisioningState)
			test.statusCode = http.StatusConflict
		}
		tests = append(tests, test)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clusterResourceID := newClusterResourceID(t)
			clusterInternalID := newClusterInternalID(t)

			requestPath := path.Join(clusterResourceID.String(), "revokeCredentials")

			reg := prometheus.NewRegistry()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			// Pre-populate the mock database with cluster
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

			cluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   clusterResourceID,
					PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{
						ID: clusterResourceID,
					},
				},
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					ProvisioningState:            test.clusterProvisioningState,
					ClusterServiceID:             &clusterInternalID,
					RevokeCredentialsOperationID: test.revokeCredentialsOperationID,
				},
			}
			_, err := mockResourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Create(ctx, cluster, nil)
			require.NoError(t, err)

			// Add active revoke operation if needed
			if test.clusterProvisioningState.IsTerminal() && len(test.revokeCredentialsOperationID) > 0 {
				operationID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperationStatusResourceTypeName + "/" + uuid.New().String()))
				resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/hcpOperationStatuses/" + uuid.New().String()))
				revokeOp := &coreapi.Operation{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   resourceID,
						PartitionKey: strings.ToLower(resourceID.SubscriptionID),
					},
					OperationID: operationID,
					Request:     cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation,
					ExternalID:  clusterResourceID,
					InternalID:  clusterInternalID,
					Status:      coreapi.ProvisioningStateDeleting,
				}
				_, err := mockResourcesDBClient.Operations(clusterResourceID.SubscriptionID).Create(ctx, revokeOp, nil)
				require.NoError(t, err)
			}

			// Add active request credential operation (will be cancelled) for success case
			if test.clusterProvisioningState.IsTerminal() && len(test.revokeCredentialsOperationID) == 0 {
				operationID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/locations/" + coreapitesting.TestLocation + "/" + coreapi.OperationStatusResourceTypeName + "/" + uuid.New().String()))
				resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestSubscriptionResourceID + "/providers/" + coreapi.ProviderNamespace + "/hcpOperationStatuses/" + uuid.New().String()))
				requestOp := &coreapi.Operation{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   resourceID,
						PartitionKey: strings.ToLower(resourceID.SubscriptionID),
					},
					OperationID: operationID,
					Request:     cosmosstorageutils.OperationRequestSystemAdminCredentialRequest,
					ExternalID:  clusterResourceID,
					InternalID:  clusterInternalID,
					Status:      coreapi.ProvisioningStateProvisioning,
				}
				_, err := mockResourcesDBClient.Operations(clusterResourceID.SubscriptionID).Create(ctx, requestOp, nil)
				require.NoError(t, err)
			}

			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			url := ts.URL + requestPath + "?api-version=" + coreapitesting.TestAPIVersion
			resp, err := ts.Client().Post(url, "", nil)
			require.NoError(t, err)

			if !assert.Equal(t, test.statusCode, resp.StatusCode) {
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				fmt.Println(string(body))
			}
		})
	}
}

func TestDeleteCluster(t *testing.T) {
	type testCase struct {
		name                           string
		clusterExists                  bool
		clusterProvisioningState       coreapi.ProvisioningState
		usesNewClusterDeletionApproach bool
		hasDeletionTimestamp           bool
		expectedStatusCode             int
		expectedErrorMessage           string
		expectDeletionTimestampSet     bool
		expectDeadlineInFuture         bool
	}

	tests := []testCase{
		{
			name:                           "Legacy cluster stuck in Deleting - no timestamp, bypass conflict check",
			clusterExists:                  true,
			clusterProvisioningState:       coreapi.ProvisioningStateDeleting,
			usesNewClusterDeletionApproach: false,
			hasDeletionTimestamp:           false, // Legacy approach doesn't set timestamp
			expectedStatusCode:             http.StatusAccepted,
			expectDeletionTimestampSet:     true,
			expectDeadlineInFuture:         true,
		},
		{
			name:                           "New approach cluster already deleting - conflict",
			clusterExists:                  true,
			clusterProvisioningState:       coreapi.ProvisioningStateDeleting,
			usesNewClusterDeletionApproach: true,
			hasDeletionTimestamp:           true, // New approach sets timestamp
			expectedStatusCode:             http.StatusConflict,
			expectedErrorMessage:           "Resource is already deleting",
		},
		{
			name:                           "Succeeded cluster - normal deletion",
			clusterExists:                  true,
			clusterProvisioningState:       coreapi.ProvisioningStateSucceeded,
			usesNewClusterDeletionApproach: false,
			hasDeletionTimestamp:           false,
			expectedStatusCode:             http.StatusAccepted,
			expectDeletionTimestampSet:     true,
			expectDeadlineInFuture:         true,
		},
		{
			name:               "Non-existent cluster - idempotent delete",
			clusterExists:      false,
			expectedStatusCode: http.StatusNoContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clusterResourceID := newClusterResourceID(t)
			clusterInternalID := newClusterInternalID(t)

			reg := prometheus.NewRegistry()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			f := NewFrontend(
				testr.New(t),
				nil,
				nil,
				reg,
				reg,
				mockResourcesDBClient,
				nil,
				newNoopAuditClient(t),
				coreapitesting.TestLocation,
				true,
			)

			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

			// Pre-populate the mock database with cluster if it should exist
			if test.clusterExists {
				cluster := &coreapi.HCPOpenShiftCluster{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   clusterResourceID,
						PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
					},
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{
							ID: clusterResourceID,
						},
					},
					ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
						ProvisioningState:              test.clusterProvisioningState,
						ClusterServiceID:               &clusterInternalID,
						UsesNewClusterDeletionApproach: test.usesNewClusterDeletionApproach,
					},
				}
				// Set DeletionTimestamp only if test specifies it should have one
				if test.hasDeletionTimestamp {
					ts := metav1.NewTime(time.Now().Add(-1 * time.Hour))
					cluster.ServiceProviderProperties.DeletionTimestamp = &ts
				}
				_, err := mockResourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
			}

			subs := map[string]*coreapi.Subscription{
				coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
			}
			ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)

			url := ts.URL + clusterResourceID.String() + "?api-version=" + coreapitesting.TestAPIVersion
			req, err := http.NewRequest(http.MethodDelete, url, nil)
			require.NoError(t, err)

			resp, err := ts.Client().Do(req)
			require.NoError(t, err)

			if !assert.Equal(t, test.expectedStatusCode, resp.StatusCode) {
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				fmt.Println(string(body))
			}

			// For conflict cases, verify error message
			if test.expectedStatusCode == http.StatusConflict && test.expectedErrorMessage != "" {
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), test.expectedErrorMessage)
			}

			// For accepted cases, verify cluster state was updated correctly
			if test.expectedStatusCode == http.StatusAccepted && test.clusterExists {
				updatedCluster, err := mockResourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
				require.NoError(t, err)

				// Verify ProvisioningState is set to Deleting
				assert.Equal(t, coreapi.ProvisioningStateDeleting, updatedCluster.ServiceProviderProperties.ProvisioningState)

				// Verify UsesNewClusterDeletionApproach is set to true
				assert.True(t, updatedCluster.ServiceProviderProperties.UsesNewClusterDeletionApproach, "UsesNewClusterDeletionApproach should be set to true after DELETE")

				// Verify ActiveOperationID is set
				assert.NotEmpty(t, updatedCluster.ServiceProviderProperties.ActiveOperationID, "ActiveOperationID should be set")

				// Verify DeletionTimestamp is set if expected
				if test.expectDeletionTimestampSet {
					require.NotNil(t, updatedCluster.ServiceProviderProperties.DeletionTimestamp, "DeletionTimestamp should be set")

					// Verify timestamp is recent (within last 5 seconds)
					timeSinceSet := time.Since(updatedCluster.ServiceProviderProperties.DeletionTimestamp.Time)
					assert.Less(t, timeSinceSet, 5*time.Second, "DeletionTimestamp should be recent")
					assert.GreaterOrEqual(t, timeSinceSet, time.Duration(0), "DeletionTimestamp should not be in future")
				}

				// Verify DeleteOperationCompletionDeadline is in the future if expected
				if test.expectDeadlineInFuture {
					require.NotNil(t, updatedCluster.ServiceProviderProperties.DeleteOperationCompletionDeadline, "DeleteOperationCompletionDeadline should be set")

					timeUntilDeadline := time.Until(updatedCluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time)
					assert.Greater(t, timeUntilDeadline, time.Duration(0), "DeleteOperationCompletionDeadline should be in the future, not expired")
					assert.Less(t, timeUntilDeadline, 13*time.Hour, "DeleteOperationCompletionDeadline should be within 12 hours (default duration) plus buffer")
				}
			}
		})
	}
}

func lintMetrics(t *testing.T, r prometheus.Gatherer) {
	t.Helper()

	problems, err := testutil.GatherAndLint(r)
	require.NoError(t, err)

	for _, p := range problems {
		t.Errorf("metric %q: %s", p.Metric, p.Text)
	}
}

// assertHTTPMetrics ensures that HTTP metrics have been recorded.
func assertHTTPMetrics(t *testing.T, r prometheus.Gatherer) {
	t.Helper()

	metrics, err := r.Gather()
	assert.NoError(t, err)

	var mfs []*dto.MetricFamily
	for _, mf := range metrics {
		if mf.GetName() != requestCounterName && mf.GetName() != requestDurationName {
			continue
		}

		mfs = append(mfs, mf)

		for _, m := range mf.GetMetric() {
			var (
				route      string
				apiVersion string
				userAgent  string
			)
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "route":
					route = l.GetValue()
				case "api_version":
					apiVersion = l.GetValue()
				case "user_agent":
					userAgent = l.GetValue()
				}
			}

			// Verify that route and API version labels have known values.
			assert.NotEmpty(t, route)
			assert.NotEqual(t, route, noMatchRouteLabel)
			assert.NotEmpty(t, apiVersion)
			assert.NotEqual(t, apiVersion, unknownVersionLabel)
			assert.Equal(t, userAgentOther, userAgent)
		}
	}

	// We need request counter and latency histogram.
	assert.Len(t, mfs, 2)
}

// newHTTPServer returns a test HTTP server. The mock DB client will be
// bootstrapped with the provided subscription documents.
func newHTTPServer(ctx context.Context, f *Frontend, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, subs map[string]*coreapi.Subscription) *httptest.Server {
	ts := httptest.NewUnstartedServer(f.server.Handler)
	ts.Config.BaseContext = f.server.BaseContext
	ts.Start()

	// Pre-populate subscriptions in the mock database
	for _, sub := range subs {
		_, _ = mockResourcesDBClient.Subscriptions().Create(ctx, sub, nil)
	}

	return ts
}
