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
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type failingDeadlineReadDesireLister struct {
	kubeapplierlisters.ReadDesireLister
}

func (failingDeadlineReadDesireLister) GetForCluster(context.Context, string, string, string, string) (*kubeapplierapi.ReadDesire, error) {
	return nil, errors.New("observation unavailable")
}

type failingDeadlineServiceProviderClusterLister struct {
	corelisters.ServiceProviderClusterLister
}

func (failingDeadlineServiceProviderClusterLister) Get(context.Context, string, string, string) (*coreapi.ServiceProviderCluster, error) {
	return nil, errors.New("service provider cluster observation unavailable")
}

func TestClusterOperationDeadlines(t *testing.T) {
	now := operationtesting.MustParseTime("2026-10-01T12:00:00Z")
	expired := &metav1.Time{Time: now.Add(-time.Second)}
	equal := &metav1.Time{Time: now}
	future := &metav1.Time{Time: now.Add(time.Second)}
	newOwner, emptyOwner := "new-owner", ""
	type expectation struct {
		operationState coreapi.ProvisioningState
		errorCode      string
		reconcileError bool
		clusterState   coreapi.ProvisioningState
		owner          string
	}
	accepted := expectation{operationState: coreapi.ProvisioningStateAccepted, clusterState: coreapi.ProvisioningStateAccepted, owner: operationtesting.TestOperationName}
	provisioning := expectation{operationState: coreapi.ProvisioningStateProvisioning, clusterState: coreapi.ProvisioningStateProvisioning, owner: operationtesting.TestOperationName}
	retry := expectation{operationState: coreapi.ProvisioningStateAccepted, reconcileError: true, clusterState: coreapi.ProvisioningStateAccepted, owner: operationtesting.TestOperationName}
	deadlineFailure := expectation{operationState: coreapi.ProvisioningStateFailed, errorCode: coreapi.CloudErrorCodeDeadlineExceeded, clusterState: coreapi.ProvisioningStateFailed}
	placementFailure := expectation{operationState: coreapi.ProvisioningStateFailed, errorCode: coreapi.CloudErrorCodeInternalServerError, clusterState: coreapi.ProvisioningStateFailed}
	mismatched := expectation{operationState: coreapi.ProvisioningStateAccepted, clusterState: coreapi.ProvisioningStateAccepted, owner: newOwner}
	unowned := expectation{operationState: coreapi.ProvisioningStateAccepted, clusterState: coreapi.ProvisioningStateAccepted}
	stalePlacement := expectation{operationState: coreapi.ProvisioningStateFailed, errorCode: coreapi.CloudErrorCodeInternalServerError, clusterState: coreapi.ProvisioningStateAccepted, owner: newOwner}
	staleDeadline := expectation{operationState: coreapi.ProvisioningStateFailed, errorCode: coreapi.CloudErrorCodeDeadlineExceeded, clusterState: coreapi.ProvisioningStateAccepted, owner: newOwner}
	succeeded := expectation{operationState: coreapi.ProvisioningStateSucceeded, clusterState: coreapi.ProvisioningStateAccepted, owner: operationtesting.TestOperationName}
	failed := expectation{operationState: coreapi.ProvisioningStateFailed, clusterState: coreapi.ProvisioningStateAccepted, owner: operationtesting.TestOperationName}
	canceled := expectation{operationState: coreapi.ProvisioningStateCanceled, clusterState: coreapi.ProvisioningStateAccepted, owner: operationtesting.TestOperationName}
	cases := []struct {
		name                  string
		deadline              *metav1.Time
		withCSID              bool
		observationErrors     bool
		owner                 *string
		storedOwner           *string
		staleOperationETag    bool
		storedOperationStatus coreapi.ProvisioningState
		operationStatus       coreapi.ProvisioningState
		clusterAbsent         bool
		// Expectations correspond to create, update, and delete. Nil means the
		// case concerns cached reads, which the delete controller does not use.
		want [3]*expectation
	}{
		{name: "expired without CS ID", deadline: expired, want: [3]*expectation{&placementFailure, &deadlineFailure, &deadlineFailure}},
		{name: "expired before prerequisites", deadline: expired, withCSID: true, want: [3]*expectation{&deadlineFailure, &deadlineFailure, &deadlineFailure}},
		{name: "expired with observation errors", deadline: expired, withCSID: true, observationErrors: true, want: [3]*expectation{&deadlineFailure, &deadlineFailure, &deadlineFailure}},
		{name: "nil deadline", want: [3]*expectation{&provisioning, &retry, &accepted}},
		{name: "at deadline", deadline: equal, want: [3]*expectation{&placementFailure, &retry, &accepted}},
		{name: "before deadline", deadline: future, want: [3]*expectation{&provisioning, &retry, &accepted}},
		{name: "ownership mismatch", deadline: expired, owner: &newOwner, want: [3]*expectation{&mismatched, &mismatched, &mismatched}},
		{name: "empty owner", deadline: expired, owner: &emptyOwner, want: [3]*expectation{&unowned, &unowned, &unowned}},
		{name: "stale cached owner", deadline: expired, storedOwner: &newOwner, want: [3]*expectation{&stalePlacement, &staleDeadline, nil}},
		{name: "stale operation etag", deadline: expired, staleOperationETag: true, want: [3]*expectation{&accepted, &accepted, nil}},
		{name: "stale terminal operation", deadline: expired, storedOperationStatus: coreapi.ProvisioningStateSucceeded, want: [3]*expectation{&succeeded, &succeeded, nil}},
		{name: "terminal succeeded", deadline: expired, operationStatus: coreapi.ProvisioningStateSucceeded, want: [3]*expectation{&succeeded, &succeeded, &succeeded}},
		{name: "terminal failed", deadline: expired, operationStatus: coreapi.ProvisioningStateFailed, want: [3]*expectation{&failed, &failed, &failed}},
		{name: "terminal canceled", deadline: expired, operationStatus: coreapi.ProvisioningStateCanceled, want: [3]*expectation{&canceled, &canceled, &canceled}},
		{name: "document missing", deadline: expired, clusterAbsent: true, want: [3]*expectation{&accepted, &accepted, &succeeded}},
	}
	for requestIndex, request := range []cosmosstorageutils.OperationRequest{
		cosmosstorageutils.OperationRequestCreate, cosmosstorageutils.OperationRequestUpdate, cosmosstorageutils.OperationRequestDelete,
	} {
		t.Run(string(request), func(t *testing.T) {
			for _, tc := range cases {
				want := tc.want[requestIndex]
				if want == nil {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
					fixture := operationtesting.NewClusterTestFixture()
					cluster := fixture.NewCluster(nil)
					cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateAccepted
					cluster.ServiceProviderProperties.ClusterServiceID = nil
					if tc.owner != nil {
						cluster.ServiceProviderProperties.ActiveOperationID = *tc.owner
					}
					if tc.withCSID {
						cluster.ServiceProviderProperties.ClusterServiceID = &fixture.ClusterInternalID
					}
					cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = tc.deadline
					cluster.ServiceProviderProperties.UpdateOperationCompletionDeadline = tc.deadline
					cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline = tc.deadline
					operation := fixture.NewOperation(request)
					if tc.operationStatus != "" {
						operation.Status = tc.operationStatus
					}
					resources := []any{operation}
					if !tc.clusterAbsent {
						resources = append(resources, cluster)
					}
					db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
					require.NoError(t, err)
					cachedOperation, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					if tc.staleOperationETag {
						cachedOperation.CosmosETag = "stale-etag"
					}
					if tc.storedOperationStatus != "" {
						completed := cachedOperation.DeepCopy()
						completed.Status = tc.storedOperationStatus
						_, err = db.Operations(operationtesting.TestSubscriptionID).Replace(ctx, completed, nil)
						require.NoError(t, err)
					}
					clusters := &corelistertesting.SliceClusterLister{}
					if !tc.clusterAbsent {
						clusters.Clusters = []*coreapi.Cluster{cluster}
					}
					if tc.storedOwner != nil {
						crud := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName)
						current, err := crud.Get(ctx, operationtesting.TestClusterName)
						require.NoError(t, err)
						current.ServiceProviderProperties.ActiveOperationID = *tc.storedOwner
						_, err = crud.Replace(ctx, current, nil)
						require.NoError(t, err)
					}
					cs := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
					if cluster.ServiceProviderProperties.ClusterServiceID != nil && request != cosmosstorageutils.OperationRequestDelete {
						if request == cosmosstorageutils.OperationRequestCreate {
							cs.EXPECT().GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).Return(nil, errors.New("CS unavailable"))
						} else {
							cs.EXPECT().GetCluster(gomock.Any(), fixture.ClusterInternalID).Return(nil, errors.New("CS unavailable"))
						}
					}
					var reads kubeapplierlisters.ReadDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
					if tc.observationErrors {
						reads = failingDeadlineReadDesireLister{}
					}
					// Delete also collects best-effort remote diagnostics on expiry.
					if request == cosmosstorageutils.OperationRequestDelete && cluster.ServiceProviderProperties.ClusterServiceID != nil {
						cs.EXPECT().GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).Return(nil, errors.New("CS unavailable"))
					}
					clock := clocktesting.NewFakePassiveClock(now)
					operations := &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{cachedOperation}}
					var spcs corelisters.ServiceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
					if tc.observationErrors {
						spcs = failingDeadlineServiceProviderClusterLister{}
					}
					switch request {
					case cosmosstorageutils.OperationRequestCreate:
						controller := &operationClusterCreate{clock: clock, resourcesDBClient: db, activeOperationLister: operations, clusterLister: clusters, serviceProviderClusterLister: spcs, readDesireLister: reads, clusterServiceClient: cs}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					case cosmosstorageutils.OperationRequestUpdate:
						controller := &operationClusterUpdate{clock: clock, resourcesDBClient: db, activeOperationsLister: operations, clusterLister: clusters, serviceProviderClusterLister: spcs, readDesireLister: reads, clusterServiceClient: cs}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					case cosmosstorageutils.OperationRequestDelete:
						controller := &operationClusterDelete{clock: clock, resourcesDBClient: db, readDesireLister: reads, clusterServiceClient: cs}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					}
					if want.reconcileError {
						require.Error(t, err, "unavailable service provider state remains retryable before expiry")
					} else {
						require.NoError(t, err)
					}
					persisted, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					assert.Equal(t, want.operationState, persisted.Status)
					if want.errorCode == "" {
						assert.Nil(t, persisted.Error)
					} else {
						require.NotNil(t, persisted.Error)
						assert.Equal(t, want.errorCode, persisted.Error.Code)
						assert.Contains(t, persisted.Error.Message, "did not complete before the deadline")
					}
					if tc.clusterAbsent {
						return
					}
					persistedCluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
					require.NoError(t, err)
					assert.Equal(t, want.owner, persistedCluster.ServiceProviderProperties.ActiveOperationID)
					assert.Equal(t, want.clusterState, persistedCluster.ServiceProviderProperties.ProvisioningState)
				})
			}
		})
	}
}
