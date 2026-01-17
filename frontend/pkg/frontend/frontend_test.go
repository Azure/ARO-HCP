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
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func getMockDBDoc[T any](t *T) (*T, error) {
	if t != nil {
		return t, nil
	} else {
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
}

func newClusterResourceID(t *testing.T) *azcorearm.ResourceID {
	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)
	return resourceID
}

func newClusterInternalID(t *testing.T) ocm.InternalID {
	internalID, err := api.NewInternalID(ocm.GenerateClusterHREF("myCluster"))
	require.NoError(t, err)
	return internalID
}

func TestSubscriptionsGET(t *testing.T) {
	tests := []struct {
		name               string
		subDoc             *arm.Subscription
		expectedStatusCode int
	}{
		{
			name: "GET Subscription - Doc Exists",
			subDoc: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
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
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockSubscriptionCRUD := mocks.NewMockSubscriptionCRUD(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				nil,
				newNoopAuditClient(t),
				api.TestLocation,
			)

			// ArmSubscriptionGet.
			mockDBClient.EXPECT().
				Subscriptions().
				Return(mockSubscriptionCRUD)
			mockSubscriptionCRUD.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(getMockDBDoc(test.subDoc)).
				Times(1)

			// The subscription collector lists all documents once.
			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[api.TestSubscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, mockSubscriptionCRUD, subs)

			rs, err := ts.Client().Get(ts.URL + api.TestSubscriptionResourceID + "?api-version=" + arm.SubscriptionAPIVersion)
			require.NoError(t, err)

			assert.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)
			assertHTTPMetrics(t, reg, test.subDoc)
		})
	}
}

func TestSubscriptionsPUT(t *testing.T) {
	tests := []struct {
		name               string
		urlPath            string
		subscription       *arm.Subscription
		subDoc             *arm.Subscription
		expectUpdated      bool
		expectedStatusCode int
	}{
		{
			name:    "PUT Subscription - Doc does not exist",
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties: &arm.SubscriptionProperties{
					TenantId: api.Ptr("12345678-1234-1234-1234-123456789abc"),
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
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			expectUpdated:      false,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Update registered features",
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties: &arm.SubscriptionProperties{
					RegisteredFeatures: &[]arm.Feature{
						{
							Name:  api.Ptr("Microsoft.RedHatOpenShift/TestFeature"),
							State: api.Ptr("Registered"),
						},
					},
				},
			},
			subDoc: &arm.Subscription{
				ResourceID:       api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			expectUpdated:      true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Invalid Subscription",
			urlPath: "/subscriptions/oopsie-i-no-good0",
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing State",
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Invalid State",
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				State:            "Bogus",
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing RegistrationDate",
			urlPath: api.TestSubscriptionResourceID,
			subscription: &arm.Subscription{
				State:      arm.SubscriptionStateRegistered,
				Properties: nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockSubscriptionCRUD := mocks.NewMockSubscriptionCRUD(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				nil,
				newNoopAuditClient(t),
				api.TestLocation,
			)

			body, err := json.Marshal(&test.subscription)
			require.NoError(t, err)

			// MiddlewareLockSubscription
			// (except when MiddlewareValidateStatic fails)
			mockDBClient.EXPECT().
				GetLockClient().
				MaxTimes(1)

			if test.expectedStatusCode != http.StatusBadRequest {
				// ArmSubscriptionPut
				mockDBClient.EXPECT().
					Subscriptions().
					Return(mockSubscriptionCRUD)
				mockSubscriptionCRUD.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(getMockDBDoc(test.subDoc))
				// ArmSubscriptionPut
				if test.subDoc == nil {
					mockDBClient.EXPECT().
						Subscriptions().
						Return(mockSubscriptionCRUD)
					mockSubscriptionCRUD.EXPECT().
						Create(gomock.Any(), gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, subscription *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
							return subscription, nil
						})
				} else if test.expectUpdated {
					mockDBClient.EXPECT().
						Subscriptions().
						Return(mockSubscriptionCRUD)
					mockSubscriptionCRUD.EXPECT().
						Replace(gomock.Any(), gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, updated *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
							assert.True(t, test.expectUpdated)
							return updated, nil
						})
				}
			}

			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[api.TestSubscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, mockSubscriptionCRUD, subs)

			urlPath := test.urlPath + "?api-version=" + arm.SubscriptionAPIVersion
			req, err := http.NewRequest(http.MethodPut, ts.URL+urlPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			rs, err := ts.Client().Do(req)
			require.NoError(t, err)

			assert.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)
			if test.expectedStatusCode != http.StatusBadRequest {
				assertHTTPMetrics(t, reg, test.subDoc)
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
		expectStatus arm.DeploymentPreflightStatus
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
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Unrecognized API version returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": "1980-01-01",
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Well-formed cluster resource returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": api.TestAPIVersion,
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "4.0",
						"channelGroup": "stable",
					},
					"api": map[string]any{
						"visibility": "Public",
					},
					"platform": map[string]any{
						"subnetId":               api.TestSubnetResourceID,
						"networkSecurityGroupId": api.TestNetworkSecurityGroupResourceID,
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches cluster resource with invalid fields",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": api.TestAPIVersion,
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
			expectStatus: arm.DeploymentPreflightStatusFailed,
			expectErrors: []expectedPreflightError{
				{message: "Invalid value: \"invalidCidr\": invalid CIDR address: invalidCidr", target: "properties.network.podCidr"},
				{message: "Unsupported value: \"invisible\": supported values: \"Private\", \"Public\"", target: "properties.api.visiblity"},
				{message: "Required value", target: "properties.platform.subnetId"},
				{message: "Required value", target: "properties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "Well-formed node pool resource returns no error",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       api.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": api.TestAPIVersion,
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						"vmSize": "Standard_D8s_v3",
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches node pool resource with invalid fields",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       api.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": api.TestAPIVersion,
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						// 1 missing required field
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
			expectStatus: arm.DeploymentPreflightStatusFailed,
			expectErrors: []expectedPreflightError{
				{message: "Required value", target: "properties.platform.vmSize"},
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
			preflightPath := path.Join(api.TestDeploymentResourceID, "preflight")

			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockSubscriptionCRUD := mocks.NewMockSubscriptionCRUD(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				nil,
				newNoopAuditClient(t),
				api.TestLocation,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				Subscriptions().
				Return(mockSubscriptionCRUD).
				MaxTimes(2)
			mockSubscriptionCRUD.EXPECT().
				Get(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: {
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, mockSubscriptionCRUD, subs)

			resource, err := json.Marshal(&test.resource)
			require.NoError(t, err)
			preflightReq := arm.DeploymentPreflight{
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

			var preflightResp arm.DeploymentPreflightResponse
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
		name                     string
		clusterProvisioningState arm.ProvisioningState
		revokeCredentialsStatus  arm.ProvisioningState
		statusCode               int
	}

	tests := []testCase{
		{
			name:                     "Request conflict: credentials revoking",
			clusterProvisioningState: arm.ProvisioningStateSucceeded,
			revokeCredentialsStatus:  arm.ProvisioningStateDeleting,
			statusCode:               http.StatusConflict,
		},
	}

	for clusterProvisioningState := range arm.ListProvisioningStates() {
		test := testCase{
			// Previously completed revocation does not interfere.
			clusterProvisioningState: clusterProvisioningState,
			revokeCredentialsStatus:  arm.ProvisioningStateSucceeded,
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

			ctrl := gomock.NewController(t)
			reg := prometheus.NewRegistry()
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)
			mockOperationCRUD := mocks.NewMockOperationCRUD(ctrl)
			mockClusterCRUD := mocks.NewMockHCPClusterCRUD(ctrl)
			mockSubscriptionCRUD := mocks.NewMockSubscriptionCRUD(ctrl)

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				mockCSClient,
				newNoopAuditClient(t),
				api.TestLocation,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				Subscriptions().
				Return(mockSubscriptionCRUD)
			mockSubscriptionCRUD.EXPECT().
				Get(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)
			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient().
				Return(nil)
			// ArmResourceActionRequestAdminCredential
			mockDBClient.EXPECT().
				HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).
				Return(mockClusterCRUD)
			mockClusterCRUD.EXPECT().
				Get(gomock.Any(), clusterResourceID.Name).
				Return(
					&api.HCPOpenShiftCluster{
						TrackedResource: arm.TrackedResource{
							Resource: arm.Resource{
								ID: clusterResourceID,
							},
						},
						ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
							ProvisioningState: test.clusterProvisioningState,
							ClusterServiceID:  clusterInternalID,
						},
					},
					nil)
			if test.clusterProvisioningState.IsTerminal() {
				revokeOperations := make(map[string]*api.Operation)
				if !test.revokeCredentialsStatus.IsTerminal() {
					revokeOperations[uuid.New().String()] = &api.Operation{
						Request:    database.OperationRequestRevokeCredentials,
						ExternalID: clusterResourceID,
						InternalID: clusterInternalID,
						Status:     test.revokeCredentialsStatus,
					}
				}
				mockOperationIter := mocks.NewMockDBClientIterator[api.Operation](ctrl)
				mockOperationIter.EXPECT().
					Items(gomock.Any()).
					Return(database.DBClientIteratorItem[api.Operation](maps.All(revokeOperations)))

				// ArmResourceActionRequestAdminCredential
				mockDBClient.EXPECT().
					Operations(clusterResourceID.SubscriptionID).
					Return(mockOperationCRUD)
				mockOperationCRUD.EXPECT().
					ListActiveOperations(gomock.Any()).
					Return(mockOperationIter)

				if test.revokeCredentialsStatus.IsTerminal() {
					mockDBTransaction := mocks.NewMockDBTransaction(ctrl)
					mockDBTransactionResult := mocks.NewMockDBTransactionResult(ctrl)

					mockOperationIter.EXPECT().
						GetError().
						Return(nil)
					// ArmResourceActionRequestAdminCredential
					mockCSClient.EXPECT().
						PostBreakGlassCredential(gomock.Any(), clusterInternalID).
						Return(cmv1.NewBreakGlassCredential().
							HREF(ocm.GenerateBreakGlassCredentialHREF(clusterInternalID.String(), "0")).Build())
					// ArmResourceActionRequestAdminCredential
					mockDBClient.EXPECT().
						NewTransaction(api.TestSubscriptionID).
						Return(mockDBTransaction)
					// ArmResourceActionRequestAdminCredential
					operationID := uuid.New().String()
					mockDBTransaction.EXPECT().
						OnSuccess(gomock.Any())
					mockDBClient.EXPECT().
						Operations(clusterResourceID.SubscriptionID).
						DoAndReturn(func(s string) database.OperationCRUD {
							return mockOperationCRUD
						})
					mockOperationCRUD.EXPECT().
						AddCreateToTransaction(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						Return(operationID, nil)

					// ArmResourceActionRequestAdminCredential
					mockDBTransaction.EXPECT().
						Execute(gomock.Any(), nil).
						Return(mockDBTransactionResult, nil)
				}
			}

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: {
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, mockSubscriptionCRUD, subs)

			url := ts.URL + requestPath + "?api-version=" + api.TestAPIVersion
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
		name                     string
		clusterProvisioningState arm.ProvisioningState
		revokeCredentialsStatus  arm.ProvisioningState
		statusCode               int
	}

	tests := []testCase{
		{
			name:                     "Request conflict: credentials revoking",
			clusterProvisioningState: arm.ProvisioningStateSucceeded,
			revokeCredentialsStatus:  arm.ProvisioningStateDeleting,
			statusCode:               http.StatusConflict,
		},
	}

	for clusterProvisioningState := range arm.ListProvisioningStates() {
		test := testCase{
			// Previously completed revocation does not interfere.
			clusterProvisioningState: clusterProvisioningState,
			revokeCredentialsStatus:  arm.ProvisioningStateSucceeded,
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

			ctrl := gomock.NewController(t)
			reg := prometheus.NewRegistry()
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)
			mockOperationCRUD := mocks.NewMockOperationCRUD(ctrl)
			mockClusterCRUD := mocks.NewMockHCPClusterCRUD(ctrl)
			mockSubscriptionCRUD := mocks.NewMockSubscriptionCRUD(ctrl)

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				mockCSClient,
				newNoopAuditClient(t),
				api.TestLocation,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				Subscriptions().
				Return(mockSubscriptionCRUD)
			mockSubscriptionCRUD.EXPECT().
				Get(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)
			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient().
				Return(nil)
			// ArmResourceActionRequestAdminCredential
			mockDBClient.EXPECT().
				HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).
				Return(mockClusterCRUD)
			mockClusterCRUD.EXPECT().
				Get(gomock.Any(), clusterResourceID.Name).
				Return(
					&api.HCPOpenShiftCluster{
						TrackedResource: arm.TrackedResource{
							Resource: arm.Resource{
								ID: clusterResourceID,
							},
						},
						ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
							ProvisioningState: test.clusterProvisioningState,
							ClusterServiceID:  clusterInternalID,
						},
					},
					nil)
			if test.clusterProvisioningState.IsTerminal() {
				revokeOperations := make(map[string]*api.Operation)
				if !test.revokeCredentialsStatus.IsTerminal() {
					revokeOperations[uuid.New().String()] = &api.Operation{
						Request:    database.OperationRequestRevokeCredentials,
						ExternalID: clusterResourceID,
						InternalID: clusterInternalID,
						Status:     test.revokeCredentialsStatus,
					}
				}
				mockOperationIter := mocks.NewMockDBClientIterator[api.Operation](ctrl)
				mockOperationIter.EXPECT().
					Items(gomock.Any()).
					Return(database.DBClientIteratorItem[api.Operation](maps.All(revokeOperations)))

				// ArmResourceActionRequestAdminCredential
				mockDBClient.EXPECT().
					Operations(clusterResourceID.SubscriptionID).
					Return(mockOperationCRUD)
				mockOperationCRUD.EXPECT().
					ListActiveOperations(gomock.Any()).
					Return(mockOperationIter)
				if test.revokeCredentialsStatus.IsTerminal() {
					mockDBTransaction := mocks.NewMockDBTransaction(ctrl)
					mockDBTransactionResult := mocks.NewMockDBTransactionResult(ctrl)

					mockOperationIter.EXPECT().
						GetError().
						Return(nil)
					// ArmResourceActionRequestAdminCredential
					mockCSClient.EXPECT().
						DeleteBreakGlassCredentials(gomock.Any(), clusterInternalID).
						Return(nil)

					requestOperationID := string(arm.ProvisioningStateProvisioning)
					requestOperations := map[string]*api.Operation{
						requestOperationID: {
							Request:    database.OperationRequestRequestCredential,
							ExternalID: clusterResourceID,
							InternalID: clusterInternalID,
							Status:     arm.ProvisioningStateProvisioning,
						},
					}
					mockOperationIter = mocks.NewMockDBClientIterator[api.Operation](ctrl)
					mockOperationIter.EXPECT().
						Items(gomock.Any()).
						Return(database.DBClientIteratorItem[api.Operation](maps.All(requestOperations)))
					mockOperationIter.EXPECT().
						GetError().
						Return(nil)

					// ArmResourceActionRequestAdminCredential
					mockDBClient.EXPECT().
						NewTransaction(api.TestSubscriptionID).
						Return(mockDBTransaction)

					// CancelActiveOperations
					mockDBTransaction.EXPECT().
						GetPartitionKey().
						Return(api.TestSubscriptionID)
					// CancelActiveOperations
					mockDBClient.EXPECT().
						Operations(clusterResourceID.SubscriptionID).
						Return(mockOperationCRUD)
					mockOperationCRUD.EXPECT().
						ListActiveOperations(gomock.Any()).
						Return(mockOperationIter)
					// CancelActiveOperations
					mockDBClient.EXPECT().
						Operations(clusterResourceID.SubscriptionID).
						Return(mockOperationCRUD)
					mockOperationCRUD.EXPECT().
						AddReplaceToTransaction(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, transaction database.DBTransaction, operation *api.Operation, options *azcosmos.TransactionalBatchItemOptions) (string, error) {
							return "", nil
						})

					// ArmResourceActionRequestAdminCredential
					operationID := uuid.New().String()
					mockDBClient.EXPECT().
						Operations(clusterResourceID.SubscriptionID).
						Return(mockOperationCRUD)
					mockOperationCRUD.EXPECT().
						AddCreateToTransaction(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, transaction database.DBTransaction, operation *api.Operation, options *azcosmos.TransactionalBatchItemOptions) (string, error) {
							return operationID, nil
						})

					// ExposeOperation
					mockDBTransaction.EXPECT().
						OnSuccess(gomock.Any())

					// ArmResourceActionRequestAdminCredential
					mockDBTransaction.EXPECT().
						Execute(gomock.Any(), nil).
						Return(mockDBTransactionResult, nil)
				}
			}

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: {
					ResourceID: api.Must(arm.ToSubscriptionResourceID(api.TestSubscriptionID)),
					State:      arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, mockSubscriptionCRUD, subs)

			url := ts.URL + requestPath + "?api-version=" + api.TestAPIVersion
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

func lintMetrics(t *testing.T, r prometheus.Gatherer) {
	t.Helper()

	problems, err := testutil.GatherAndLint(r)
	require.NoError(t, err)

	for _, p := range problems {
		t.Errorf("metric %q: %s", p.Metric, p.Text)
	}
}

// assertHTTPMetrics ensures that HTTP metrics have been recorded.
func assertHTTPMetrics(t *testing.T, r prometheus.Gatherer, subscription *arm.Subscription) {
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
				state      string
			)
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "route":
					route = l.GetValue()
				case "api_version":
					apiVersion = l.GetValue()
				case "state":
					state = l.GetValue()
				}
			}

			// Verify that route and API version labels have known values.
			assert.NotEmpty(t, route)
			assert.NotEqual(t, route, noMatchRouteLabel)
			assert.NotEmpty(t, apiVersion)
			assert.NotEqual(t, apiVersion, unknownVersionLabel)

			if mf.GetName() == requestCounterName {
				assert.NotEmpty(t, state)
				if subscription != nil {
					assert.Equal(t, string(subscription.State), state)
				} else {
					assert.Equal(t, "Unknown", state)
				}
			}
		}
	}

	// We need request counter and latency histogram.
	assert.Len(t, mfs, 2)
}

// newHTTPServer returns a test HTTP server. When a mock DB client is provided,
// the subscription collector will be bootstrapped with the provided
// subscription documents.
func newHTTPServer(f *Frontend, ctrl *gomock.Controller, mockDBClient *mocks.MockDBClient, mockSubscriptionCRUD *mocks.MockSubscriptionCRUD, subs map[string]*arm.Subscription) *httptest.Server {
	ts := httptest.NewUnstartedServer(f.server.Handler)
	ts.Config.BaseContext = f.server.BaseContext
	ts.Start()

	mockIter := mocks.NewMockDBClientIterator[arm.Subscription](ctrl)
	mockIter.EXPECT().
		Items(gomock.Any()).
		Return(database.DBClientIteratorItem[arm.Subscription](maps.All(subs)))

	mockIter.EXPECT().
		GetError().
		Return(nil)

	mockDBClient.EXPECT().
		Subscriptions().
		Return(mockSubscriptionCRUD)
	mockSubscriptionCRUD.EXPECT().
		List(gomock.Any(), gomock.Any()).
		Return(mockIter, nil).
		Times(1)

	// The initialization of the subscriptions collector is normally part of
	// the Run() method but the method doesn't get called in the tests so it's
	// executed here.
	stop := make(chan struct{})
	close(stop)
	f.collector.Run(api.NewTestLogger(), stop)

	return ts
}
