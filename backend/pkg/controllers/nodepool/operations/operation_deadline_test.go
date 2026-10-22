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
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type failingDeadlineReadDesireLister struct {
	kubeapplierlisters.ReadDesireLister
}

func (failingDeadlineReadDesireLister) GetForNodePool(context.Context, string, string, string, string, string) (*kubeapplierapi.ReadDesire, error) {
	return nil, errors.New("observation unavailable")
}

func TestNodePoolOperationDeadlines(t *testing.T) {
	now := operationtesting.MustParseTime("2026-10-01T12:00:00Z")
	for _, request := range []cosmosstorageutils.OperationRequest{
		cosmosstorageutils.OperationRequestCreate, cosmosstorageutils.OperationRequestUpdate, cosmosstorageutils.OperationRequestDelete,
	} {
		t.Run(string(request), func(t *testing.T) {
			for _, scenario := range []string{
				"expired without CS ID", "expired before prerequisites", "expired with observation errors",
				"nil deadline", "at deadline", "before deadline", "ownership mismatch", "empty owner",
				"stale cached owner", "stale operation etag", "stale terminal operation", "terminal succeeded", "terminal failed", "terminal canceled", "document missing",
			} {
				if request == cosmosstorageutils.OperationRequestDelete && (scenario == "stale cached owner" || scenario == "stale operation etag" || scenario == "stale terminal operation") {
					continue // Delete reads operation and resource directly from the database.
				}
				t.Run(scenario, func(t *testing.T) {
					ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
					fixture := operationtesting.NewNodePoolTestFixture()
					nodePool := fixture.NewNodePool()
					nodePool.Properties.ProvisioningState = coreapi.ProvisioningStateAccepted
					nodePool.ServiceProviderProperties.ClusterServiceID = nil
					deadline := &metav1.Time{Time: now.Add(-time.Second)}
					switch scenario {
					case "nil deadline":
						deadline = nil
					case "at deadline":
						deadline.Time = now
					case "before deadline":
						deadline.Time = now.Add(time.Second)
					case "ownership mismatch":
						nodePool.ServiceProviderProperties.ActiveOperationID = "new-owner"
					case "empty owner":
						nodePool.ServiceProviderProperties.ActiveOperationID = ""
					case "expired before prerequisites":
						nodePool.ServiceProviderProperties.ClusterServiceID = &fixture.NodePoolInternalID
					case "expired with observation errors":
						nodePool.ServiceProviderProperties.ClusterServiceID = &fixture.NodePoolInternalID
					}
					nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline = deadline
					nodePool.ServiceProviderProperties.UpdateOperationCompletionDeadline = deadline
					nodePool.ServiceProviderProperties.DeleteOperationCompletionDeadline = deadline
					operation := fixture.NewOperation(request)
					want := coreapi.ProvisioningStateFailed
					switch scenario {
					case "nil deadline", "at deadline", "before deadline", "ownership mismatch", "empty owner", "stale operation etag":
						want = coreapi.ProvisioningStateAccepted
						if request == cosmosstorageutils.OperationRequestCreate && (scenario == "nil deadline" || scenario == "at deadline" || scenario == "before deadline") {
							want = coreapi.ProvisioningStateProvisioning
						}
					case "terminal succeeded":
						operation.Status = coreapi.ProvisioningStateSucceeded
						want = operation.Status
					case "terminal failed":
						operation.Status = coreapi.ProvisioningStateFailed
						want = operation.Status
					case "terminal canceled":
						operation.Status = coreapi.ProvisioningStateCanceled
						want = operation.Status
					case "document missing":
						want = coreapi.ProvisioningStateAccepted
						if request == cosmosstorageutils.OperationRequestDelete {
							want = coreapi.ProvisioningStateSucceeded
						}
					}
					resources := []any{operation}
					if scenario != "document missing" {
						resources = append(resources, nodePool)
					}
					db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
					require.NoError(t, err)
					cachedOperation, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					if scenario == "stale operation etag" {
						cachedOperation.CosmosETag = "stale-etag"
					}
					if scenario == "stale terminal operation" {
						completed := cachedOperation.DeepCopy()
						completed.Status = coreapi.ProvisioningStateSucceeded
						_, err = db.Operations(operationtesting.TestSubscriptionID).Replace(ctx, completed, nil)
						require.NoError(t, err)
						want = coreapi.ProvisioningStateSucceeded
					}
					nodePools := &corelistertesting.SliceNodePoolLister{}
					if scenario != "document missing" {
						nodePools.NodePools = []*coreapi.NodePool{nodePool}
					}
					crud := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).NodePools(operationtesting.TestClusterName)
					if scenario == "stale cached owner" {
						current, err := crud.Get(ctx, operationtesting.TestNodePoolName)
						require.NoError(t, err)
						current.ServiceProviderProperties.ActiveOperationID = "new-owner"
						_, err = crud.Replace(ctx, current, nil)
						require.NoError(t, err)
					}
					cs := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
					if nodePool.ServiceProviderProperties.ClusterServiceID != nil && request != cosmosstorageutils.OperationRequestDelete {
						if request == cosmosstorageutils.OperationRequestCreate {
							cs.EXPECT().GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).Return(nil, errors.New("CS unavailable"))
						} else {
							cs.EXPECT().GetNodePool(gomock.Any(), fixture.NodePoolInternalID).Return(nil, errors.New("CS unavailable"))
						}
					}
					var reads kubeapplierlisters.ReadDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
					if scenario == "expired with observation errors" {
						reads = failingDeadlineReadDesireLister{}
					}
					clock := clocktesting.NewFakePassiveClock(now)
					operations := &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{cachedOperation}}
					switch request {
					case cosmosstorageutils.OperationRequestCreate:
						controller := &operationNodePoolCreate{clock: clock, resourcesDBClient: db, activeOperationsLister: operations, nodePoolLister: nodePools, readDesireLister: reads, clusterServiceClient: cs}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					case cosmosstorageutils.OperationRequestUpdate:
						controller := &operationNodePoolUpdate{clock: clock, resourcesDBClient: db, activeOperationsLister: operations, nodePoolLister: nodePools, readDesireLister: reads, clusterServiceClient: cs, serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{}}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					case cosmosstorageutils.OperationRequestDelete:
						controller := &operationNodePoolDelete{clock: clock, resourcesDBClient: db, clusterServiceClient: cs}
						err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
					}
					if request == cosmosstorageutils.OperationRequestUpdate && (scenario == "nil deadline" || scenario == "at deadline" || scenario == "before deadline") {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					persisted, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					assert.Equal(t, want, persisted.Status)
					if operation.Status.IsTerminal() || want != coreapi.ProvisioningStateFailed {
						assert.Nil(t, persisted.Error)
					} else {
						require.NotNil(t, persisted.Error)
						assert.Equal(t, coreapi.CloudErrorCodeDeadlineExceeded, persisted.Error.Code)
						assert.Contains(t, persisted.Error.Message, "did not complete before the deadline")
					}
					if scenario == "document missing" {
						return
					}
					persistedNodePool, err := crud.Get(ctx, operationtesting.TestNodePoolName)
					require.NoError(t, err)
					if scenario == "stale cached owner" {
						assert.Equal(t, "new-owner", persistedNodePool.ServiceProviderProperties.ActiveOperationID)
						assert.Equal(t, coreapi.ProvisioningStateAccepted, persistedNodePool.Properties.ProvisioningState)
					} else if want == coreapi.ProvisioningStateFailed && !operation.Status.IsTerminal() {
						assert.Empty(t, persistedNodePool.ServiceProviderProperties.ActiveOperationID)
						assert.Equal(t, want, persistedNodePool.Properties.ProvisioningState)
					} else {
						assert.Equal(t, nodePool.ServiceProviderProperties.ActiveOperationID, persistedNodePool.ServiceProviderProperties.ActiveOperationID)
						resourceState := coreapi.ProvisioningStateAccepted
						if want == coreapi.ProvisioningStateProvisioning {
							resourceState = want
						}
						assert.Equal(t, resourceState, persistedNodePool.Properties.ProvisioningState)
					}
				})
			}
		})
	}
}
