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
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolDelete_SynchronizeOperation(t *testing.T) {
	fixture := operationtesting.NewNodePoolTestFixture()

	nodePoolPassingExtraReconcileGate := func() *coreapi.HCPOpenShiftClusterNodePool {
		now := time.Now()
		np := fixture.NewNodePool()
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return np
	}

	testCases := []struct {
		name                    string
		existingNodePool        *coreapi.HCPOpenShiftClusterNodePool
		wantErr                 bool
		verifyDB                func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
		usesNewDeletionApproach bool
		setupCSMock             func(ctrl *gomock.Controller, fixture *operationtesting.NodePoolTestFixture) ocm.ClusterServiceClientSpec
	}{
		{
			name: "node pool document gone completes operation",
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
			usesNewDeletionApproach: true,
		},
		{
			name: "shouldReconcile gate not passed skips cluster service",
			existingNodePool: func() *coreapi.HCPOpenShiftClusterNodePool {
				np := fixture.NewNodePool()
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return np
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
			usesNewDeletionApproach: true,
		},
		{
			name:             "extra reconcilegate passed and CS Ready waits without updating operation",
			existingNodePool: nodePoolPassingExtraReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.NodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(operationbase.NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
			usesNewDeletionApproach: true,
		},
		{
			name:             "extra reconcile gate passed and CS uninstalling updates operation to deleting",
			existingNodePool: nodePoolPassingExtraReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.NodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(operationbase.NodePoolStateUninstalling))).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status)
			},
			usesNewDeletionApproach: true,
		},
		{
			name: "legacy approach: node pool gone in cluster service marks operation succeeded",
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.NodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
			usesNewDeletionApproach: false,
		},
		{
			name: "legacy approach: node pool still exists in cluster service keeps operation accepted",
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.NodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(operationbase.NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
			usesNewDeletionApproach: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
			// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
			operation.UsesNewNodePoolDeletionApproach = tc.usesNewDeletionApproach

			resources := []any{operation}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
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

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
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
