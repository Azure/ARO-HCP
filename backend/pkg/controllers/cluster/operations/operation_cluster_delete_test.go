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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterDelete_SynchronizeOperation(t *testing.T) {
	fixedTime := operationtesting.MustParseTime("2025-01-20T10:30:00Z")
	createdAt := operationtesting.MustParseTime("2025-01-15T10:30:00Z")

	fixture := operationtesting.NewClusterTestFixture()

	clusterPassingReconcileGate := func() *coreapi.Cluster {
		now := time.Now()
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return cluster
	}

	testCases := []struct {
		name            string
		nodePools       []*coreapi.NodePool
		externalAuths   []*coreapi.ExternalAuth
		existingCluster *coreapi.Cluster
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
			existingCluster: func() *coreapi.Cluster {
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
			existingCluster: func() *coreapi.Cluster {
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

			mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()
			if tc.existingCluster != nil {
				billingDoc := billingcosmosstorage.NewBillingDocument(tc.existingCluster.ServiceProviderProperties.ClusterUID, fixture.ClusterResourceID)
				billingDoc.CreationTime = createdAt
				billingDoc.Location = operationtesting.TestAzureLocation
				billingDoc.TenantID = operationtesting.TestTenantID
				err := mockBillingDBClient.BillingDocs(fixture.ClusterResourceID.SubscriptionID).Create(ctx, billingDoc)
				require.NoError(t, err)
			}

			resources := []any{operation}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			for _, nodePool := range tc.nodePools {
				resources = append(resources, nodePool)
			}
			for _, externalAuth := range tc.externalAuths {
				resources = append(resources, externalAuth)
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
				billingDBClient:      mockBillingDBClient,
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

type unavailableDeletionClients struct {
	err    error
	client kubeappliercosmosstorage.KubeApplierDBClient
}

func (clients unavailableDeletionClients) For(context.Context, *azcorearm.ResourceID) (kubeappliercosmosstorage.KubeApplierDBClient, error) {
	return clients.client, clients.err
}

type diagnosticResources struct {
	corecosmosstorage.ResourcesDBClient
	spcErr error
}

func (db diagnosticResources) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return diagnosticSPCCRUD{ResourceCRUD: db.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName), err: db.spcErr}
}

type diagnosticSPCCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	err error
}

func (crud diagnosticSPCCRUD) Get(ctx context.Context, name string) (*coreapi.ServiceProviderCluster, error) {
	if crud.err != nil {
		return nil, crud.err
	}
	return crud.ResourceCRUD.Get(ctx, name)
}

type diagnosticClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient
	crud cosmosstorageutils.UntypedResourceCRUD
	err  error
}

func (client diagnosticClient) UntypedCRUD(azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return client.crud, client.err
}

type diagnosticCRUD struct {
	cosmosstorageutils.UntypedResourceCRUD
	iterator cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument]
	err      error
}

func (crud diagnosticCRUD) ListRecursive(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	return crud.iterator, crud.err
}

type diagnosticIterator struct {
	cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument]
	err error
}

func (iterator diagnosticIterator) GetError() error { return iterator.err }

func TestDeletionDiagnosticsQueryFailures(t *testing.T) {
	injected := errors.New("inventory query failed")
	type testCase struct {
		name        string
		spcErr      error
		crudErr     error
		listErr     error
		iteratorErr error
		missingSPC  bool
		wantErr     error
	}
	for _, testCase := range []testCase{
		{name: "SPC read", spcErr: injected, wantErr: injected},
		{name: "kube-applier CRUD", crudErr: injected, wantErr: injected},
		{name: "list descendants", listErr: injected, wantErr: injected},
		{name: "partial iterator", iteratorErr: injected, wantErr: injected},
		{name: "successful inventory"},
		{name: "absent SPC", missingSPC: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()
			fixture := operationtesting.NewClusterTestFixture()
			cluster := fixture.NewCluster(nil)
			cluster.ServiceProviderProperties.ClusterServiceID = nil
			deadline := metav1.NewTime(time.Unix(100, 0))
			cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline = &deadline
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
			mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default"))
			spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/serviceProviderClusters/default")), PartitionKey: cluster.ID.SubscriptionID}, Status: coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mcID}}
			resources := []any{cluster, operation}
			if !testCase.missingSPC {
				resources = append(resources, spc)
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			iterator := diagnosticIterator{DBClientIterator: corecosmosstoragetesting.NewMockIterator([]string{"child"}, []*cosmosstorageutils.TypedDocument{{ResourceType: "test/children"}}), err: testCase.iteratorErr}
			client := diagnosticClient{crud: diagnosticCRUD{iterator: iterator, err: testCase.listErr}, err: testCase.crudErr}
			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(deadline.Add(time.Second)),
				resourcesDBClient:    diagnosticResources{ResourcesDBClient: db, spcErr: testCase.spcErr},
				kubeApplierDBClients: unavailableDeletionClients{client: client},
				readDesireLister:     &kubeapplierlistertesting.SliceReadDesireLister{},
				applyDesireLister:    &kubeapplierlistertesting.SliceApplyDesireLister{},
			}
			state, err := controller.remainingDescendantResources(ctx, cluster)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				require.Nil(t, state)
			} else {
				require.NoError(t, err)
				require.NotNil(t, state)
			}
			require.NoError(t, controller.SynchronizeOperation(ctx, fixture.OperationKey()))
			updated, err := db.Operations(fixture.ClusterResourceID.SubscriptionID).Get(ctx, operationtesting.TestOperationName)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateFailed, updated.Status)
			require.NotNil(t, updated.Error)
			require.Contains(t, updated.Error.Message, "cluster deletion did not complete before the deadline")
			if testCase.wantErr != nil {
				require.NotContains(t, updated.Error.Message, "[descendantResources]")
			}
			_, err = db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Get(ctx, cluster.ID.Name)
			require.NoError(t, err)
			if !testCase.missingSPC {
				_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
			}
		})
	}
}

func TestDeletionDiagnosticsUnavailableClient(t *testing.T) {
	cluster := operationtesting.NewClusterTestFixture().NewCluster(nil)
	mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default"))
	spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/serviceProviderClusters/default")), PartitionKey: cluster.ID.SubscriptionID}, Status: coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mcID}}
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), []any{cluster, spc})
	require.NoError(t, err)
	injected := errors.New("client resolution failed")
	for _, failure := range []error{nil, injected} {
		controller := &operationClusterDelete{resourcesDBClient: db, kubeApplierDBClients: unavailableDeletionClients{err: failure}}
		require.NotPanics(t, func() {
			state, err := controller.remainingDescendantResources(t.Context(), cluster)
			require.Nil(t, state)
			if failure != nil {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorContains(t, err, "inventory unavailable")
			}
		})
	}
}
