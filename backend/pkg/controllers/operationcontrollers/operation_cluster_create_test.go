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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
	testOperationName       = "test-operation-id"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testAzureLocation       = "eastus"
)

// testFixture contains common test objects and helpers
type testFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         api.InternalID
}

func newTestFixture() *testFixture {
	return &testFixture{
		clusterResourceID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
		)),
		operationID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
				"/operationstatuses/" + testOperationName,
		)),
		cosmosOperationResourceID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
		)),
		clusterInternalID: api.Must(api.NewInternalID(testClusterServiceIDStr)),
	}
}

func (f *testFixture) newCluster(createdAt *time.Time) *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   f.clusterResourceID,
				Name: testClusterName,
				Type: f.clusterResourceID.ResourceType.String(),
				SystemData: &arm.SystemData{
					CreatedAt: createdAt,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID:  f.clusterInternalID,
			ActiveOperationID: testOperationName,
		},
	}
}

func (f *testFixture) newOperation() *api.Operation {
	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: f.cosmosOperationResourceID,
		},
		TenantID:    testTenantID,
		Status:      arm.ProvisioningStateAccepted,
		Request:     database.OperationRequestCreate,
		ExternalID:  f.clusterResourceID,
		InternalID:  f.clusterInternalID,
		OperationID: f.operationID,
	}
}

func (f *testFixture) operationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID: testSubscriptionID,
		OperationName:  testOperationName,
	}
}

func TestSynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2025, 1, 20, 10, 30, 0, 0, time.UTC)
	createdAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name         string
		clusterState arohcpv1alpha1.ClusterState
		createdAt    *time.Time
		expectError  bool
		verify       func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *testFixture)
	}{
		{
			name:         "succeeds with valid CreatedAt time",
			clusterState: arohcpv1alpha1.ClusterStateReady,
			createdAt:    &createdAt,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *testFixture) {
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
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *testFixture) {
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
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *testFixture) {
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

			fixture := newTestFixture()
			cluster := fixture.newCluster(tt.createdAt)
			operation := fixture.newOperation()

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
