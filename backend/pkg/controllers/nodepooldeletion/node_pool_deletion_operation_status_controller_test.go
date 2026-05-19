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

package nodepooldeletion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testOperationName = "test-operation"
	testAzureLocation = "eastus"
)

func TestNodePoolDeletionOperationStatus_SyncOnce(t *testing.T) {
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	withDeletionStamps := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
	}

	testCases := []struct {
		name               string
		existingNodePool   *api.HCPOpenShiftClusterNodePool
		existingOperation  *api.Operation
		expectGetStatus    bool
		csStatus           *arohcpv1alpha1.NodePoolStatus
		getStatusErr       error
		wantOperationState arm.ProvisioningState
		wantErr            bool
		wantErrContain     string
	}{
		{
			name:             "no DeletionTimestamp — no-op",
			existingNodePool: newTestNodePool(t, nil),
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not yet — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
		},
		{
			name: "ClusterServiceID already cleared — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
		},
		{
			name: "no active operation — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = ""
			}),
		},
		{
			name: "active operation is not a delete — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = testOperationName
			}),
			existingOperation: func() *api.Operation {
				op := newTestOperation(t)
				op.Request = database.OperationRequestCreate
				return op
			}(),
		},
		{
			name: "CS returns uninstalling — operation updated to Deleting",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = testOperationName
			}),
			existingOperation:  newTestOperation(t),
			expectGetStatus:    true,
			csStatus:           newCSNodePoolStatus(t, string(operationcontrollers.NodePoolStateUninstalling)),
			wantOperationState: arm.ProvisioningStateDeleting,
		},
		{
			name: "CS returns error — operation updated to Failed",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = testOperationName
			}),
			existingOperation:  newTestOperation(t),
			expectGetStatus:    true,
			csStatus:           newCSNodePoolStatusWithMessage(t, string(operationcontrollers.NodePoolStateError), "delete failed"),
			wantOperationState: arm.ProvisioningStateFailed,
		},
		{
			name: "CS returns 404 — no operation update",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = testOperationName
			}),
			existingOperation: newTestOperation(t),
			expectGetStatus:   true,
			getStatusErr:      fakeOCMNotFoundError(),
		},
		{
			name: "CS returns transient error — propagated",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ActiveOperationID = testOperationName
			}),
			existingOperation: newTestOperation(t),
			expectGetStatus:   true,
			getStatusErr:      errors.New("boom"),
			wantErr:           true,
			wantErrContain:    "failed to get cluster-service NodePool status",
		},
		{
			name: "node pool not found — no-op",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			if tc.existingOperation != nil {
				resources = append(resources, tc.existingOperation)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectGetStatus {
				call := mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr)))
				if tc.getStatusErr != nil {
					call.Return(nil, tc.getStatusErr)
				} else {
					call.Return(tc.csStatus, nil)
				}
			}

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			syncer := &nodePoolDeletionOperationStatusController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			}

			err = syncer.SyncOnce(ctx, key)
			if tc.wantErr {
				require.Error(t, err)
				require.Greater(t, len(tc.wantErrContain), 0, "wantErrContain must be set when wantErr is true")
				assert.ErrorContains(t, err, tc.wantErrContain)
				return
			}
			require.NoError(t, err)

			if tc.existingOperation != nil && tc.wantOperationState != "" {
				op, err := mockResourcesDBClient.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, tc.wantOperationState, op.Status)
			}
		})
	}
}

func newCSNodePoolStatus(t *testing.T, state string) *arohcpv1alpha1.NodePoolStatus {
	t.Helper()
	s, err := arohcpv1alpha1.NewNodePoolStatus().
		State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(state)).
		Build()
	require.NoError(t, err)
	return s
}

func newCSNodePoolStatusWithMessage(t *testing.T, state, message string) *arohcpv1alpha1.NodePoolStatus {
	t.Helper()
	s, err := arohcpv1alpha1.NewNodePoolStatus().
		State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(state)).
		Message(message).
		Build()
	require.NoError(t, err)
	return s
}

func newTestOperation(t *testing.T) *api.Operation {
	t.Helper()
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
			"/operationstatuses/" + testOperationName))
	cosmosOperationResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName))
	nodePoolInternalID := api.Must(api.NewInternalID(testNodePoolCSIDStr))

	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: cosmosOperationResourceID,
		},
		Status:      arm.ProvisioningStateAccepted,
		Request:     database.OperationRequestDelete,
		ExternalID:  nodePoolResourceID,
		InternalID:  nodePoolInternalID,
		OperationID: operationID,
	}
}
