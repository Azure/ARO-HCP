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
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterCreate_SynchronizeOperation(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	tests := []struct {
		name         string
		clusterState arohcpv1alpha1.ClusterState
		createdAt    *time.Time
		expectError  bool
		verify       func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture)
	}{
		{
			name:         "succeeds with valid CreatedAt time",
			clusterState: arohcpv1alpha1.ClusterStateReady,
			createdAt:    &createdAt,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				// Verify operation status
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify billing document was created
				billingDocs := db.GetBillingDocuments()
				require.Len(t, billingDocs, 1, "expected one billing document to be created")
				for _, doc := range billingDocs {
					assert.Equal(t, testTenantID, doc.TenantID)
					assert.Equal(t, testAzureLocation, doc.Location)
					assert.Equal(t, createdAt, doc.CreationTime)
				}
			},
		},
		{
			name:         "succeeds with nil CreatedAt using fallback time",
			clusterState: arohcpv1alpha1.ClusterStateReady,
			createdAt:    nil,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				// Verify operation status
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify billing document was created with fallback time
				billingDocs := db.GetBillingDocuments()
				require.Len(t, billingDocs, 1, "expected one billing document to be created")
				for _, doc := range billingDocs {
					assert.Equal(t, testTenantID, doc.TenantID)
					assert.Equal(t, testAzureLocation, doc.Location)
					assert.Equal(t, fixedTime, doc.CreationTime, "should use fallback time when CreatedAt is nil")
				}
			},
		},
		{
			name:         "non-terminal cluster state updates to provisioning without billing",
			clusterState: arohcpv1alpha1.ClusterStateInstalling,
			createdAt:    nil,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				// Verify operation status
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)

				// Verify no billing document was created
				billingDocs := db.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created for non-terminal state")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(tt.createdAt)
			operation := fixture.newOperation(database.OperationRequestCreate)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			require.NoError(t, err)

			mockCSClient.EXPECT().
				GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
				Return(clusterStatus, nil)

			controller := &operationClusterCreate{
				clock:                clocktesting.NewFakePassiveClock(fixedTime),
				azureLocation:        testAzureLocation,
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, fixture)
			}
		})
	}
}
