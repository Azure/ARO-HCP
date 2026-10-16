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
					ctx := context.Background()
					fixture := operationtesting.NewClusterTestFixture()
					cluster := fixture.NewCluster(nil)
					cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateAccepted
					cluster.ServiceProviderProperties.ClusterServiceID = nil
					deadline := &metav1.Time{Time: now.Add(-time.Second)}
					switch scenario {
					case "nil deadline":
						deadline = nil
					case "at deadline":
						deadline.Time = now
					case "before deadline":
						deadline.Time = now.Add(time.Second)
					case "ownership mismatch":
						cluster.ServiceProviderProperties.ActiveOperationID = "new-owner"
					case "empty owner":
						cluster.ServiceProviderProperties.ActiveOperationID = ""
					case "expired before prerequisites", "expired with observation errors":
						cluster.ServiceProviderProperties.ClusterServiceID = &fixture.ClusterInternalID
					}
					cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = deadline
					cluster.ServiceProviderProperties.UpdateOperationCompletionDeadline = deadline
					cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline = deadline
					operation := fixture.NewOperation(request)
					want := coreapi.ProvisioningStateFailed
					switch scenario {
					case "nil deadline", "at deadline", "before deadline":
						want = coreapi.ProvisioningStateAccepted
						if request == cosmosstorageutils.OperationRequestCreate {
							want = coreapi.ProvisioningStateProvisioning
						}
					case "ownership mismatch", "empty owner", "stale operation etag":
						want = coreapi.ProvisioningStateAccepted
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
						resources = append(resources, cluster)
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
					clusters := &corelistertesting.SliceClusterLister{}
					if scenario != "document missing" {
						clusters.Clusters = []*coreapi.Cluster{cluster}
					}
					if scenario == "stale cached owner" {
						crud := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName)
						current, err := crud.Get(ctx, operationtesting.TestClusterName)
						require.NoError(t, err)
						current.ServiceProviderProperties.ActiveOperationID = "new-owner"
						_, err = crud.Replace(ctx, current, nil)
						require.NoError(t, err)
					}
					cs := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
					var reads kubeapplierlisters.ReadDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
					if scenario == "expired with observation errors" {
						reads = failingDeadlineReadDesireLister{}
					}
					// Only cluster delete retains best-effort remote timeout diagnostics.
					if request == cosmosstorageutils.OperationRequestDelete && cluster.ServiceProviderProperties.ClusterServiceID != nil {
						cs.EXPECT().GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).Return(nil, errors.New("CS unavailable"))
					}
					clock := clocktesting.NewFakePassiveClock(now)
					operations := &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{cachedOperation}}
					var spcs corelisters.ServiceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
					if scenario == "expired with observation errors" {
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
					require.NoError(t, err)
					persisted, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					assert.Equal(t, want, persisted.Status)
					if operation.Status.IsTerminal() || want != coreapi.ProvisioningStateFailed {
						assert.Nil(t, persisted.Error)
					} else {
						require.NotNil(t, persisted.Error)
						code := coreapi.CloudErrorCodeDeadlineExceeded
						if request == cosmosstorageutils.OperationRequestDelete {
							code = coreapi.CloudErrorCodeInternalServerError
						}
						assert.Equal(t, code, persisted.Error.Code)
						assert.Contains(t, persisted.Error.Message, "did not complete before the deadline")
					}
					if scenario == "document missing" {
						return
					}
					persistedCluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
					require.NoError(t, err)
					if scenario == "stale cached owner" {
						assert.Equal(t, "new-owner", persistedCluster.ServiceProviderProperties.ActiveOperationID)
						assert.Equal(t, coreapi.ProvisioningStateAccepted, persistedCluster.ServiceProviderProperties.ProvisioningState)
					} else if want == coreapi.ProvisioningStateFailed && !operation.Status.IsTerminal() {
						assert.Empty(t, persistedCluster.ServiceProviderProperties.ActiveOperationID)
						assert.Equal(t, want, persistedCluster.ServiceProviderProperties.ProvisioningState)
					} else if want == coreapi.ProvisioningStateAccepted || operation.Status.IsTerminal() || scenario == "stale terminal operation" {
						assert.Equal(t, cluster.ServiceProviderProperties.ActiveOperationID, persistedCluster.ServiceProviderProperties.ActiveOperationID)
						assert.Equal(t, coreapi.ProvisioningStateAccepted, persistedCluster.ServiceProviderProperties.ProvisioningState)
					}
				})
			}
		})
	}
}
