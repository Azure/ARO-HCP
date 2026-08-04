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

package operations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRequestCredentialPoll_ShouldProcess(t *testing.T) {
	syncer := &operationRequestCredentialPoll{
		clock: utilsclock.RealClock{},
	}

	credResourceID := api.Must(azcorearm.ParseResourceID(
		api.ToSystemAdminCredentialRequestResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName),
	))

	tests := []struct {
		name     string
		op       *api.Operation
		expected bool
	}{
		{
			name: "terminal operation should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateSucceeded,
				Request: api.OperationRequestSystemAdminCredentialRequest,
				SystemAdminCredentialRequest: &api.OperationSystemAdminCredentialRequest{
					SystemAdminCredentialRequestResourceID: credResourceID,
				},
			},
			expected: false,
		},
		{
			name: "wrong request type should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: "Create",
			},
			expected: false,
		},
		{
			name: "nil SystemAdminCredentialRequest should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: api.OperationRequestSystemAdminCredentialRequest,
			},
			expected: false,
		},
		{
			name: "nil SystemAdminCredentialRequestResourceID should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: api.OperationRequestSystemAdminCredentialRequest,
				SystemAdminCredentialRequest: &api.OperationSystemAdminCredentialRequest{
					CertificateSigningRequest: "test-csr",
				},
			},
			expected: false,
		},
		{
			name: "valid operation with credential resource ID should process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: api.OperationRequestSystemAdminCredentialRequest,
				SystemAdminCredentialRequest: &api.OperationSystemAdminCredentialRequest{
					SystemAdminCredentialRequestResourceID: credResourceID,
				},
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := syncer.ShouldProcess(context.Background(), tc.op)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestOperationRequestCredentialPoll_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	credResourceID := api.Must(azcorearm.ParseResourceID(
		api.ToSystemAdminCredentialRequestResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName),
	))

	tests := []struct {
		name        string
		resources   []any
		setupDB     func(t *testing.T, db *databasetesting.MockResourcesDBClient)
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:        "operation not found returns nil",
			resources:   nil,
			expectError: false,
		},
		{
			name: "pending credential request updates operation to Provisioning",
			resources: func() []any {
				op := newTestOperation(api.OperationRequestSystemAdminCredentialRequest)
				op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = credResourceID
				return []any{op}
			}(),
			setupDB: func(t *testing.T, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
				cred := &api.SystemAdminCredentialRequest{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID:   credResourceID,
						PartitionKey: strings.ToLower(testSubscriptionID),
					},
					Spec: api.SystemAdminCredentialRequestSpec{
						Username:    "test-user",
						OperationID: testOperationName,
					},
				}
				_, err := credCRUD.Create(context.Background(), cred, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status, "pending credential should set operation to Provisioning")
			},
		},
		{
			name: "issued credential request updates operation to Succeeded",
			resources: func() []any {
				op := newTestOperation(api.OperationRequestSystemAdminCredentialRequest)
				op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = credResourceID
				return []any{op}
			}(),
			setupDB: func(t *testing.T, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
				cred := &api.SystemAdminCredentialRequest{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID:   credResourceID,
						PartitionKey: strings.ToLower(testSubscriptionID),
					},
					Spec: api.SystemAdminCredentialRequestSpec{
						Username:    "test-user",
						OperationID: testOperationName,
					},
				}
				meta.SetStatusCondition(&cred.Status.Conditions, metav1.Condition{
					Type:    api.SystemAdminCredentialRequestConditionIssued,
					Status:  metav1.ConditionTrue,
					Reason:  "Issued",
					Message: "credential issued",
				})
				_, err := credCRUD.Create(context.Background(), cred, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status, "issued credential should set operation to Succeeded")
			},
		},
		{
			name: "failed credential request updates operation to Failed",
			resources: func() []any {
				op := newTestOperation(api.OperationRequestSystemAdminCredentialRequest)
				op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = credResourceID
				return []any{op}
			}(),
			setupDB: func(t *testing.T, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
				cred := &api.SystemAdminCredentialRequest{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID:   credResourceID,
						PartitionKey: strings.ToLower(testSubscriptionID),
					},
					Spec: api.SystemAdminCredentialRequestSpec{
						Username:    "test-user",
						OperationID: testOperationName,
					},
				}
				meta.SetStatusCondition(&cred.Status.Conditions, metav1.Condition{
					Type:    api.SystemAdminCredentialRequestConditionFailed,
					Status:  metav1.ConditionTrue,
					Reason:  "Failed",
					Message: "CSR was denied",
				})
				_, err := credCRUD.Create(context.Background(), cred, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status, "failed credential should set operation to Failed")
				require.NotNil(t, op.Error, "failed operation should have an error body")
				assert.Equal(t, "CSR was denied", op.Error.Message, "error message should come from the Failed condition")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tc.resources)
			require.NoError(t, err)

			if tc.setupDB != nil {
				tc.setupDB(t, db)
			}

			syncer := &operationRequestCredentialPoll{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			key := controllerutils.OperationKey{
				SubscriptionID: testSubscriptionID,
				OperationName:  testOperationName,
			}

			err = syncer.SynchronizeOperation(ctx, key)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.verify != nil {
				tc.verify(t, ctx, db)
			}
		})
	}
}
