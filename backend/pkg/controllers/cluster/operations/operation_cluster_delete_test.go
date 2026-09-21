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
	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterDelete_SynchronizeOperation(t *testing.T) {
	fixedTime := operationtesting.MustParseTime("2025-01-20T10:30:00Z")

	fixture := operationtesting.NewClusterTestFixture()

	clusterPassingReconcileGate := func() *coreapi.HCPOpenShiftCluster {
		now := time.Now()
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return cluster
	}

	testCases := []struct {
		name            string
		existingCluster *coreapi.HCPOpenShiftCluster
		setupCSMock     func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		wantErr         bool
		verifyDB        func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "cluster document gone completes operation",
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed skips cluster service",
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := fixture.NewCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := fixture.NewCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:            "reconcile gate passed and CS uninstalling updates operation to deleting",
			existingCluster: clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name:            "reconcile gate passed and CS error marks operation failed",
			existingCluster: clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateError).
					ProvisionErrorCode("ERR001").
					ProvisionErrorMessage("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)
			},
		},
		{
			name:            "reconcile gate passed and CS Ready waits for Cosmos deletion",
			existingCluster: clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for Cosmos Cluster document deletion")
			},
		},
		{
			name:            "reconcile gate passed and CS 404 skips operation update",
			existingCluster: clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for ID clearer")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)

			resources := []any{operation}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(fixedTime),
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
