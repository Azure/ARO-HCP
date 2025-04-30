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
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

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
		return nil, database.ErrNotFound
	}
}

func newClusterResourceID(t *testing.T) *azcorearm.ResourceID {
	resourceID, err := azcorearm.ParseResourceID(path.Join(
		"/",
		"subscriptions", api.TestSubscriptionID,
		"resourceGroups", "myResourceGroup",
		"providers", api.ProviderNamespace,
		api.ClusterResourceTypeName, "myCluster"))
	require.NoError(t, err)
	return resourceID
}

func equalResourceID(expectResourceID *azcorearm.ResourceID) gomock.Matcher {
	return gomock.Cond(func(actualResourceID *azcorearm.ResourceID) bool {
		return strings.EqualFold(actualResourceID.String(), expectResourceID.String())
	})
}

func equalListActiveOperationDocsOptions(expectRequest database.OperationRequest, expectExternalID *azcorearm.ResourceID) gomock.Matcher {
	return gomock.Cond(func(actualOptions *database.DBClientListActiveOperationDocsOptions) bool {
		return actualOptions != nil &&
			actualOptions.Request != nil && *actualOptions.Request == expectRequest &&
			strings.EqualFold(actualOptions.ExternalID.String(), expectExternalID.String())
	})
}

func newClusterInternalID(t *testing.T) ocm.InternalID {
	internalID, err := ocm.NewInternalID(ocm.GenerateClusterHREF("myCluster"))
	require.NoError(t, err)
	return internalID
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name               string
		ready              bool
		expectedStatusCode int
	}{
		{
			name:               "Not ready - returns 500",
			ready:              false,
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:               "Ready - returns 200",
			ready:              true,
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)
			f.ready.Store(test.ready)

			mockDBClient.EXPECT().DBConnectionTest(gomock.Any())

			ts := newHTTPServer(f, ctrl, mockDBClient, nil)

			rs, err := ts.Client().Get(ts.URL + "/healthz")
			require.NoError(t, err)
			require.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)

			got, err := testutil.GatherAndCount(reg, healthGaugeName)
			require.NoError(t, err)
			assert.Equal(t, 1, got)
		})
	}
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
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)

			// ArmSubscriptionGet.
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), gomock.Any()).
				Return(getMockDBDoc(test.subDoc)).
				Times(1)

			// The subscription collector lists all documents once.
			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[api.TestSubscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			rs, err := ts.Client().Get(ts.URL + "/subscriptions/" + api.TestSubscriptionID + "?api-version=" + arm.SubscriptionAPIVersion)
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
		expectedStatusCode int
	}{
		{
			name:    "PUT Subscription - Doc does not exist",
			urlPath: "/subscriptions/" + api.TestSubscriptionID,
			subscription: &arm.Subscription{
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
			name:    "PUT Subscription - Doc Exists",
			urlPath: "/subscriptions/" + api.TestSubscriptionID,
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
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
			urlPath: "/subscriptions/" + api.TestSubscriptionID,
			subscription: &arm.Subscription{
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Invalid State",
			urlPath: "/subscriptions/" + api.TestSubscriptionID,
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
			urlPath: "/subscriptions/" + api.TestSubscriptionID,
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
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
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
					GetSubscriptionDoc(gomock.Any(), gomock.Any()).
					Return(getMockDBDoc(test.subDoc))
				// ArmSubscriptionPut
				if test.subDoc == nil {
					mockDBClient.EXPECT().
						CreateSubscriptionDoc(gomock.Any(), gomock.Any(), gomock.Any())
				} else {
					mockDBClient.EXPECT().
						UpdateSubscriptionDoc(gomock.Any(), gomock.Any(), gomock.Any())
				}
			}

			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[api.TestSubscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

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

func TestDeploymentPreflight(t *testing.T) {
	tests := []struct {
		name         string
		resource     map[string]any
		expectStatus arm.DeploymentPreflightStatus
		expectErrors int
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
						"id":           "4.0.0",
						"channelGroup": "stable",
					},
					"api": map[string]any{
						"visibility": "public",
					},
					"platform": map[string]any{
						"subnetId":               "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/virtualNetworks/MyVNet/subnets",
						"networkSecurityGroupId": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/networkSecurityGroups/MyNSG",
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
			expectErrors: 4,
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
							// 1 invalid + 1 missing required field
							"effect": "NoTouchy",
						},
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusFailed,
			expectErrors: 4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preflightPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/myRG/providers/%s/deployments/myDeployment/preflight", api.TestSubscriptionID, api.ProviderNamespace)

			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: &arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

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
			switch test.expectErrors {
			case 0:
				assert.Nil(t, preflightResp.Error)
			case 1:
				if assert.NotNil(t, preflightResp.Error) {
					assert.Nil(t, preflightResp.Error.Details)
					assert.NotEmpty(t, preflightResp.Error.Code)
					assert.NotEmpty(t, preflightResp.Error.Message)
					assert.NotEmpty(t, preflightResp.Error.Target)
				}
			default:
				if assert.NotNil(t, preflightResp.Error) {
					assert.Equal(t, test.expectErrors, len(preflightResp.Error.Details))
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
			pk := database.NewPartitionKey(api.TestSubscriptionID)

			requestPath := path.Join(clusterResourceID.String(), "requestAdminCredential")

			ctrl := gomock.NewController(t)
			reg := prometheus.NewRegistry()
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				mockCSClient,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)
			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient().
				Return(nil)
			// ArmResourceActionRequestAdminCredential
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(clusterResourceID)).
				Return(getMockDBDoc(&database.ResourceDocument{
					ResourceID:        clusterResourceID,
					InternalID:        clusterInternalID,
					ProvisioningState: test.clusterProvisioningState,
				}))
			if test.clusterProvisioningState.IsTerminal() {
				revokeOperations := make(map[string]*database.OperationDocument)
				if !test.revokeCredentialsStatus.IsTerminal() {
					revokeOperations[uuid.New().String()] = &database.OperationDocument{
						Request:    database.OperationRequestRevokeCredentials,
						ExternalID: clusterResourceID,
						InternalID: clusterInternalID,
						Status:     test.revokeCredentialsStatus,
					}
				}
				mockOperationIter := mocks.NewMockDBClientIterator[database.OperationDocument](ctrl)
				mockOperationIter.EXPECT().
					Items(gomock.Any()).
					Return(database.DBClientIteratorItem[database.OperationDocument](maps.All(revokeOperations)))

				// ArmResourceActionRequestAdminCredential
				mockDBClient.EXPECT().
					ListActiveOperationDocs(gomock.Any(), equalListActiveOperationDocsOptions(database.OperationRequestRevokeCredentials, clusterResourceID)).
					Return(mockOperationIter)
				if test.revokeCredentialsStatus.IsTerminal() {
					mockOperationIter.EXPECT().
						GetError().
						Return(nil)
					// ArmResourceActionRequestAdminCredential
					mockCSClient.EXPECT().
						PostBreakGlassCredential(gomock.Any(), clusterInternalID).
						Return(cmv1.NewBreakGlassCredential().
							HREF(ocm.GenerateBreakGlassCredentialHREF(clusterInternalID.String(), "0")).Build())
					// ArmResourceActionRequestAdminCredential
					operationID := uuid.New().String()
					mockDBClient.EXPECT().
						CreateOperationDoc(gomock.Any(), gomock.Any()).
						Return(operationID, nil)
					// ArmResourceActionRequestAdminCredential
					mockDBClient.EXPECT().
						UpdateOperationDoc(gomock.Any(), pk, operationID, gomock.Any()).
						Return(true, nil)
				}
			}

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: &arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

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
			pk := database.NewPartitionKey(api.TestSubscriptionID)

			requestPath := path.Join(clusterResourceID.String(), "revokeCredentials")

			ctrl := gomock.NewController(t)
			reg := prometheus.NewRegistry()
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				mockCSClient,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), api.TestSubscriptionID).
				Return(&arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)
			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient().
				Return(nil)
			// ArmResourceActionRequestAdminCredential
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(clusterResourceID)).
				Return(getMockDBDoc(&database.ResourceDocument{
					ResourceID:        clusterResourceID,
					InternalID:        clusterInternalID,
					ProvisioningState: test.clusterProvisioningState,
				}))
			if test.clusterProvisioningState.IsTerminal() {
				revokeOperations := make(map[string]*database.OperationDocument)
				if !test.revokeCredentialsStatus.IsTerminal() {
					revokeOperations[uuid.New().String()] = &database.OperationDocument{
						Request:    database.OperationRequestRevokeCredentials,
						ExternalID: clusterResourceID,
						InternalID: clusterInternalID,
						Status:     test.revokeCredentialsStatus,
					}
				}
				mockOperationIter := mocks.NewMockDBClientIterator[database.OperationDocument](ctrl)
				mockOperationIter.EXPECT().
					Items(gomock.Any()).
					Return(database.DBClientIteratorItem[database.OperationDocument](maps.All(revokeOperations)))

				// ArmResourceActionRequestAdminCredential
				mockDBClient.EXPECT().
					ListActiveOperationDocs(gomock.Any(), equalListActiveOperationDocsOptions(database.OperationRequestRevokeCredentials, clusterResourceID)).
					Return(mockOperationIter)
				if test.revokeCredentialsStatus.IsTerminal() {
					mockOperationIter.EXPECT().
						GetError().
						Return(nil)
					// ArmResourceActionRequestAdminCredential
					mockCSClient.EXPECT().
						DeleteBreakGlassCredentials(gomock.Any(), clusterInternalID).
						Return(nil)

					requestOperations := map[string]*database.OperationDocument{
						string(arm.ProvisioningStateProvisioning): &database.OperationDocument{
							Request:    database.OperationRequestRequestCredential,
							ExternalID: clusterResourceID,
							InternalID: clusterInternalID,
							Status:     arm.ProvisioningStateProvisioning,
						},
					}
					mockOperationIter = mocks.NewMockDBClientIterator[database.OperationDocument](ctrl)
					mockOperationIter.EXPECT().
						Items(gomock.Any()).
						Return(database.DBClientIteratorItem[database.OperationDocument](maps.All(requestOperations)))
					mockOperationIter.EXPECT().
						GetError().
						Return(nil)

					// ArmResourceActionRequestAdminCredential
					mockDBClient.EXPECT().
						ListActiveOperationDocs(gomock.Any(), equalListActiveOperationDocsOptions(database.OperationRequestRequestCredential, clusterResourceID)).
						Return(mockOperationIter)
					// CancelOperation
					mockDBClient.EXPECT().
						UpdateOperationDoc(gomock.Any(), pk, string(arm.ProvisioningStateProvisioning), gomock.Any()).
						Return(true, nil)

					// ArmResourceActionRequestAdminCredential
					operationID := uuid.New().String()
					mockDBClient.EXPECT().
						CreateOperationDoc(gomock.Any(), gomock.Any()).
						Return(operationID, nil)
					// ArmResourceActionRequestAdminCredential
					mockDBClient.EXPECT().
						UpdateOperationDoc(gomock.Any(), pk, operationID, gomock.Any()).
						Return(true, nil)
				}
			}

			subs := map[string]*arm.Subscription{
				api.TestSubscriptionID: &arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

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
func newHTTPServer(f *Frontend, ctrl *gomock.Controller, mockDBClient *mocks.MockDBClient, subs map[string]*arm.Subscription) *httptest.Server {
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
		ListAllSubscriptionDocs().
		Return(mockIter).
		Times(1)

	// The initialization of the subscriptions collector is normally part of
	// the Run() method but the method doesn't get called in the tests so it's
	// executed here.
	stop := make(chan struct{})
	close(stop)
	f.collector.Run(api.NewTestLogger(), stop)

	return ts
}
