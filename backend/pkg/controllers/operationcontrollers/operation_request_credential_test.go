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

package operationcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRequestCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name              string
		operationOverride func(*api.Operation)
		expectedResult    bool
	}{
		{
			name:              "Accepted status should be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateAccepted },
			expectedResult:    true,
		},
		{
			name:              "Terminal ProvisioningState should not be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			expectedResult:    false,
		},
		{
			name:              "Wrong operation request type should not be processed",
			operationOverride: func(o *api.Operation) { o.Request = database.OperationRequestRevokeCredentials },
			expectedResult:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			fixture := newClusterTestFixture()
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			operation.Status = arm.ProvisioningStateAccepted
			operation.InternalID = api.Must(api.NewInternalID(testBreakGlassCredentialIDStr))
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			controller := &operationRequestCredential{}
			result := controller.ShouldProcess(ctx, operation)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestOperationRequestCredential_SynchronizeOperation(t *testing.T) {
	breakGlassID := api.Must(api.NewInternalID(testBreakGlassCredentialIDStr))

	tests := []struct {
		name              string
		operationOverride func(*api.Operation)
		cred              *api.ClusterAdminCredential
		includeCred       bool
		expectError       bool
		verify            func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name:        "created credential updates operation status to provisioning",
			includeCred: true,
			cred: &api.ClusterAdminCredential{
				Status: api.ClusterAdminCredentialStatusCreated,
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:        "failed credential updates operation status to failed",
			includeCred: true,
			cred: &api.ClusterAdminCredential{
				Status: api.ClusterAdminCredentialStatusFailed,
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.Equal(t, arm.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:        "issued credential with kubeconfig updates operation status to succeeded",
			includeCred: true,
			cred: &api.ClusterAdminCredential{
				Status:              api.ClusterAdminCredentialStatusIssued,
				Kubeconfig:          "kubeconfig-data",
				ExpirationTimestamp: time.Now().Add(time.Hour),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:        "issued credential without kubeconfig stays provisioning",
			includeCred: true,
			cred: &api.ClusterAdminCredential{
				Status: api.ClusterAdminCredentialStatusIssued,
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:        "unhandled status leads to error",
			includeCred: true,
			cred: &api.ClusterAdminCredential{
				Status: "CompleteFantasy",
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:        "missing credential doc fails the operation",
			includeCred: false,
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
			},
		},
		{
			name:              "ShouldProcess returns false for terminal status and no state change occurs",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			includeCred:       false,
			expectError:       false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			operation.InternalID = breakGlassID
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			resources := []any{cluster, operation}
			if tt.includeCred {
				cred, err := database.NewClusterAdminCredential(cluster.ID, breakGlassID, operation.OperationID.Name)
				require.NoError(t, err)
				cred.Status = tt.cred.Status
				cred.Kubeconfig = tt.cred.Kubeconfig
				cred.ExpirationTimestamp = tt.cred.ExpirationTimestamp
				resources = append(resources, cred)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			controller := &operationRequestCredential{
				clock:             utilsclock.RealClock{},
				resourcesDBClient: mockResourcesDBClient,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
