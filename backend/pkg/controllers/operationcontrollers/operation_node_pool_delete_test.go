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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolDelete_SynchronizeOperation(t *testing.T) {
	fixture := newNodePoolTestFixture()

	nodePoolPassingExtraReconcileGate := func() *api.HCPOpenShiftClusterNodePool {
		now := time.Now()
		np := fixture.newNodePool()
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return np
	}

	testCases := []struct {
		name             string
		existingNodePool *api.HCPOpenShiftClusterNodePool
		wantErr          bool
		verifyDB         func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		setupCSMock      func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec
	}{
		{
			name: "node pool document gone completes operation",
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed skips cluster service",
			existingNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return np
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:             "extra reconcilegate passed and CS Ready waits without updating operation",
			existingNodePool: nodePoolPassingExtraReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:             "extra reconcile gate passed and CS uninstalling updates operation to deleting",
			existingNodePool: nodePoolPassingExtraReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateUninstalling))).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.newOperation(database.OperationRequestDelete)
			resources := []any{operation}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationNodePoolDelete{
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
