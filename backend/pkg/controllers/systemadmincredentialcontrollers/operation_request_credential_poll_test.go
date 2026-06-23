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

package systemadmincredentialcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestMapCredentialPhaseToARMStatus(t *testing.T) {
	tests := []struct {
		name          string
		phase         api.SystemAdminCredentialPhase
		wantStatus    arm.ProvisioningState
		wantCloudErr  bool
	}{
		{
			name:         "Requested maps to Provisioning",
			phase:        api.SystemAdminCredentialPhaseRequested,
			wantStatus:   arm.ProvisioningStateProvisioning,
			wantCloudErr: false,
		},
		{
			name:         "Issued maps to Succeeded",
			phase:        api.SystemAdminCredentialPhaseIssued,
			wantStatus:   arm.ProvisioningStateSucceeded,
			wantCloudErr: false,
		},
		{
			name:         "Failed maps to Failed with error",
			phase:        api.SystemAdminCredentialPhaseFailed,
			wantStatus:   arm.ProvisioningStateFailed,
			wantCloudErr: true,
		},
		{
			name:         "AwaitingRevocation maps to Provisioning",
			phase:        api.SystemAdminCredentialPhaseAwaitingRevocation,
			wantStatus:   arm.ProvisioningStateProvisioning,
			wantCloudErr: false,
		},
		{
			name:         "Revoked maps to Failed with conflict error",
			phase:        api.SystemAdminCredentialPhaseRevoked,
			wantStatus:   arm.ProvisioningStateFailed,
			wantCloudErr: true,
		},
		{
			name:         "unknown phase defaults to Provisioning",
			phase:        api.SystemAdminCredentialPhase("SomethingElse"),
			wantStatus:   arm.ProvisioningStateProvisioning,
			wantCloudErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred := testCredential(tt.phase)
			status, cloudErr := mapCredentialPhaseToARMStatus(cred)
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantCloudErr {
				assert.NotNil(t, cloudErr)
			} else {
				assert.Nil(t, cloudErr)
			}
		})
	}
}

func TestOperationRequestCredentialPoll_ShouldProcess(t *testing.T) {
	tests := []struct {
		name   string
		op     *api.Operation
		expect bool
	}{
		{
			name: "accepts non-terminal RequestCredential with SystemAdminCredential InternalID",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateProvisioning)
				credRID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))
				op.InternalID = api.Must(api.NewInternalID(credRID.String()))
				return op
			}(),
			expect: true,
		},
		{
			name: "rejects terminal operation",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateSucceeded)
				credRID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))
				op.InternalID = api.Must(api.NewInternalID(credRID.String()))
				return op
			}(),
			expect: false,
		},
		{
			name:   "rejects wrong operation type",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateProvisioning),
			expect: false,
		},
		{
			name:   "rejects empty InternalID",
			op:     testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateProvisioning),
			expect: false,
		},
		{
			name: "rejects InternalID of wrong kind",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateProvisioning)
				// cluster-service style internal ID
				op.InternalID = api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
				return op
			}(),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &operationRequestCredentialPoll{}
			got := syncer.ShouldProcess(context.Background(), tt.op)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOperationRequestCredentialPoll_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	makeOp := func(phase api.SystemAdminCredentialPhase) (*api.Operation, *api.SystemAdminCredential) {
		cred := testCredential(phase)
		credRID := cred.GetResourceID()
		op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateProvisioning)
		op.InternalID = api.Must(api.NewInternalID(credRID.String()))
		return op, cred
	}

	tests := []struct {
		name     string
		phase    api.SystemAdminCredentialPhase
		wantErr  bool
		verifyDB func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:    "Requested credential keeps operation at Provisioning",
			phase:   api.SystemAdminCredentialPhaseRequested,
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:    "Issued credential moves operation to Succeeded",
			phase:   api.SystemAdminCredentialPhaseIssued,
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:    "Failed credential moves operation to Failed",
			phase:   api.SystemAdminCredentialPhaseFailed,
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)
			fakeClock := clocktesting.NewFakePassiveClock(fixedTime)

			op, cred := makeOp(tt.phase)
			cluster := testCluster()
			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op})
			require.NoError(t, err)
			createCredentialInDB(ctx, t, cred, db)

			syncer := &operationRequestCredentialPoll{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			err = syncer.SynchronizeOperation(ctx, testOperationKey())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, db)
			}
		})
	}
}
