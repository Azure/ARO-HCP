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

package frontend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterDeleteRetryPreservesTeardown(t *testing.T) {
	customTimeout := 30 * time.Minute
	for _, tc := range []struct {
		name    string
		timeout *time.Duration
		legacy  bool
	}{
		{name: "default timeout"},
		{name: "custom timeout", timeout: &customTimeout},
		{name: "legacy deletion with existing timestamp", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
			clock := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			f := &Frontend{resourcesDBClient: db, azureLocation: coreapitesting.TestLocation, clock: clock}

			cluster := coreapitesting.MinimumValidClusterTestCase()
			cluster.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: cluster.ID, PartitionKey: cluster.ID.SubscriptionID}
			cluster.ServiceProviderProperties.DeleteOperationCompletionTimeout = tc.timeout
			if tc.legacy {
				timestamp := metav1.NewTime(clock.Now().Add(-25 * time.Hour))
				cluster.ServiceProviderProperties.DeletionTimestamp = &timestamp
				cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateDeleting
			}
			clusters := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName)
			cluster, err := clusters.Create(ctx, cluster, nil)
			require.NoError(t, err)

			nodePoolID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/nodePools/worker")
			require.NoError(t, err)
			nodePool := &coreapi.NodePool{
				CosmosMetadata:  coreapi.CosmosMetadata{ResourceID: nodePoolID, PartitionKey: cluster.ID.SubscriptionID},
				TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: nodePoolID}},
			}
			nodePools := clusters.NodePools(cluster.ID.Name)
			_, err = nodePools.Create(ctx, nodePool, nil)
			require.NoError(t, err)

			externalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
			externalAuthID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/externalAuths/auth")
			require.NoError(t, err)
			externalAuth.ID = externalAuthID
			externalAuth.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: externalAuthID, PartitionKey: cluster.ID.SubscriptionID}
			externalAuths := clusters.ExternalAuth(cluster.ID.Name)
			_, err = externalAuths.Create(ctx, externalAuth, nil)
			require.NoError(t, err)

			transaction := db.NewTransaction(cluster.ID.SubscriptionID)
			require.NoError(t, f.addDeleteClusterToTransaction(ctx, nil, nil, transaction, cluster))
			_, err = transaction.Execute(ctx, nil)
			require.NoError(t, err)

			cluster, err = clusters.Get(ctx, cluster.ID.Name)
			require.NoError(t, err)
			deletionTimestamp := cluster.ServiceProviderProperties.DeletionTimestamp.DeepCopy()
			duration := admission.DefaultDeleteOperationCompletionDeadlineDuration
			if tc.timeout != nil {
				duration = *tc.timeout
			}
			require.WithinDuration(t, clock.Now().Add(duration), cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time, 0)
			nodePool, err = nodePools.Get(ctx, nodePoolID.Name)
			require.NoError(t, err)
			require.NotNil(t, nodePool.ServiceProviderProperties.DeletionTimestamp)
			require.NotEmpty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			externalAuth, err = externalAuths.Get(ctx, externalAuthID.Name)
			require.NoError(t, err)
			require.NotNil(t, externalAuth.ServiceProviderProperties.DeletionTimestamp)
			require.NotEmpty(t, externalAuth.ServiceProviderProperties.ActiveOperationID)
			nodePoolOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, nodePool.ServiceProviderProperties.ActiveOperationID)
			require.NoError(t, err)
			externalAuthOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, externalAuth.ServiceProviderProperties.ActiveOperationID)
			require.NoError(t, err)

			dispatchTime := metav1.NewTime(clock.Now().Add(time.Minute))
			cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &dispatchTime
			cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateFailed
			cluster, err = clusters.Replace(ctx, cluster, nil)
			require.NoError(t, err)
			oldOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, cluster.ServiceProviderProperties.ActiveOperationID)
			require.NoError(t, err)
			oldOperation.Status = coreapi.ProvisioningStateFailed
			oldOperation, err = db.Operations(cluster.ID.SubscriptionID).Replace(ctx, oldOperation, nil)
			require.NoError(t, err)
			clock.Step(25 * time.Hour)

			lateNodePoolID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/nodePools/late")
			require.NoError(t, err)
			lateNodePool := coreapi.NewDefaultNodePool(lateNodePoolID, coreapitesting.TestLocation)
			lateNodePool.SetResourceID(lateNodePoolID)
			lateNodePool.SetPartitionKey(cluster.ID.SubscriptionID)
			_, err = nodePools.Create(ctx, lateNodePool, nil)
			require.NoError(t, err)
			lateExternalAuthID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/externalAuths/late")
			require.NoError(t, err)
			lateExternalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
			lateExternalAuth.ID = lateExternalAuthID
			lateExternalAuth.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: lateExternalAuthID, PartitionKey: cluster.ID.SubscriptionID}
			_, err = externalAuths.Create(ctx, lateExternalAuth, nil)
			require.NoError(t, err)

			transaction = db.NewTransaction(cluster.ID.SubscriptionID)
			require.NoError(t, f.addDeleteClusterToTransaction(ctx, nil, nil, transaction, cluster))
			_, err = transaction.Execute(ctx, nil)
			require.NoError(t, err)

			cluster, err = clusters.Get(ctx, cluster.ID.Name)
			require.NoError(t, err)
			require.WithinDuration(t, clock.Now().Add(duration), cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time, 0, "retry must receive the full configured monitoring timeout")
			require.Equal(t, deletionTimestamp, cluster.ServiceProviderProperties.DeletionTimestamp, "retry must preserve deletion intent")
			require.True(t, dispatchTime.Equal(cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp), "retry must preserve Cluster Service dispatch progress")
			require.Equal(t, coreapi.ProvisioningStateDeleting, cluster.ServiceProviderProperties.ProvisioningState)
			require.NotEqual(t, oldOperation.ResourceID.Name, cluster.ServiceProviderProperties.ActiveOperationID)
			storedOldOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, oldOperation.ResourceID.Name)
			require.NoError(t, err)
			require.Equal(t, oldOperation, storedOldOperation, "completed operations must remain unchanged")
			storedNodePool, err := nodePools.Get(ctx, nodePoolID.Name)
			require.NoError(t, err)
			require.Equal(t, nodePool, storedNodePool, "retry must not replace the child node pool or its operation ID")
			storedExternalAuth, err := externalAuths.Get(ctx, externalAuthID.Name)
			require.NoError(t, err)
			require.Equal(t, externalAuth, storedExternalAuth, "retry must not replace the child external auth or its operation ID")
			storedNodePoolOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, nodePoolOperation.ResourceID.Name)
			require.NoError(t, err)
			require.Equal(t, nodePoolOperation, storedNodePoolOperation, "retry must not cancel the child node pool operation")
			storedExternalAuthOperation, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, externalAuthOperation.ResourceID.Name)
			require.NoError(t, err)
			require.Equal(t, externalAuthOperation, storedExternalAuthOperation, "retry must not cancel the child external auth operation")
			lateNodePool, err = nodePools.Get(ctx, lateNodePoolID.Name)
			require.NoError(t, err)
			require.NotNil(t, lateNodePool.ServiceProviderProperties.DeletionTimestamp, "retry must initialize children created after the parent failed")
			require.True(t, lateNodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach)
			require.NotEmpty(t, lateNodePool.ServiceProviderProperties.ActiveOperationID)
			lateExternalAuth, err = externalAuths.Get(ctx, lateExternalAuthID.Name)
			require.NoError(t, err)
			require.NotNil(t, lateExternalAuth.ServiceProviderProperties.DeletionTimestamp)
			require.True(t, lateExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach)
			require.NotEmpty(t, lateExternalAuth.ServiceProviderProperties.ActiveOperationID)
		})
	}
}

type failingChildListDB struct {
	corecosmosstorage.ResourcesDBClient
	kind     string
	err      error
	executed bool
}

func (d *failingChildListDB) HCPClusters(subscriptionID, resourceGroup string) corecosmosstorage.HCPClusterCRUD {
	return failingChildListClusterCRUD{d.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroup), d}
}

func (d *failingChildListDB) NewTransaction(partitionKey string) cosmosstorageutils.DBTransaction {
	transaction := d.ResourcesDBClient.NewTransaction(partitionKey)
	transaction.OnSuccess(func(cosmosstorageutils.DBTransactionResult) { d.executed = true })
	return transaction
}

type failingChildListClusterCRUD struct {
	corecosmosstorage.HCPClusterCRUD
	db *failingChildListDB
}

func (c failingChildListClusterCRUD) NodePools(name string) corecosmosstorage.NodePoolsCRUD {
	return failingNodePoolListCRUD{c.HCPClusterCRUD.NodePools(name), c.db}
}

func (c failingChildListClusterCRUD) ExternalAuth(name string) corecosmosstorage.ExternalAuthsCRUD {
	return failingExternalAuthListCRUD{c.HCPClusterCRUD.ExternalAuth(name), c.db}
}

type failingNodePoolListCRUD struct {
	corecosmosstorage.NodePoolsCRUD
	db *failingChildListDB
}

func (c failingNodePoolListCRUD) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.NodePool], error) {
	items, err := c.NodePoolsCRUD.List(ctx, options)
	if err != nil || c.db.kind != "node pool" {
		return items, err
	}
	return &failingChildIterator[coreapi.NodePool]{DBClientIterator: items, failure: c.db.err}, nil
}

type failingExternalAuthListCRUD struct {
	corecosmosstorage.ExternalAuthsCRUD
	db *failingChildListDB
}

func (c failingExternalAuthListCRUD) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.ExternalAuth], error) {
	items, err := c.ExternalAuthsCRUD.List(ctx, options)
	if err != nil || c.db.kind != "external auth" {
		return items, err
	}
	return &failingChildIterator[coreapi.ExternalAuth]{DBClientIterator: items, failure: c.db.err}, nil
}

type failingChildIterator[T any] struct {
	cosmosstorageutils.DBClientIterator[T]
	failure error
	err     error
}

func (i *failingChildIterator[T]) Items(ctx context.Context) cosmosstorageutils.DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		for id, item := range i.DBClientIterator.Items(ctx) {
			if !yield(id, item) {
				return
			}
			i.err = i.failure
			return
		}
	}
}

func (i *failingChildIterator[T]) GetError() error {
	return i.err
}

func TestClusterDeleteChildListFailure(t *testing.T) {
	for _, kind := range []string{"node pool", "external auth"} {
		for _, legacy := range []bool{false, true} {
			name := kind + "/initial"
			if legacy {
				name = kind + "/legacy"
			}
			t.Run(name, func(t *testing.T) {
				ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
				ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				cluster := coreapitesting.MinimumValidClusterTestCase()
				cluster.SetResourceID(cluster.ID)
				cluster.SetPartitionKey(cluster.ID.SubscriptionID)
				if legacy {
					cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateDeleting
				}
				clusters := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName)
				cluster, err := clusters.Create(ctx, cluster, nil)
				require.NoError(t, err)
				nodePoolID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/nodePools/worker")
				require.NoError(t, err)
				nodePool := coreapi.NewDefaultNodePool(nodePoolID, coreapitesting.TestLocation)
				nodePool.SetResourceID(nodePool.ID)
				nodePool.SetPartitionKey(cluster.ID.SubscriptionID)
				nodePool, err = clusters.NodePools(cluster.ID.Name).Create(ctx, nodePool, nil)
				require.NoError(t, err)
				externalAuthID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/externalAuths/auth")
				require.NoError(t, err)
				externalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
				externalAuth.ID = externalAuthID
				externalAuth.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: externalAuthID, PartitionKey: cluster.ID.SubscriptionID}
				externalAuth, err = clusters.ExternalAuth(cluster.ID.Name).Create(ctx, externalAuth, nil)
				require.NoError(t, err)
				listFailure := errors.New("child listing failed after first item")
				failingDB := &failingChildListDB{ResourcesDBClient: db, kind: kind, err: listFailure}
				f := &Frontend{resourcesDBClient: failingDB, azureLocation: coreapitesting.TestLocation, clock: clocktesting.NewFakeClock(time.Now())}
				ctx = utils.ContextWithResourceID(ctx, cluster.ID)
				request := httptest.NewRequestWithContext(ctx, http.MethodDelete, cluster.ID.String(), nil)
				require.ErrorIs(t, f.DeleteCluster(httptest.NewRecorder(), request), listFailure)
				require.False(t, failingDB.executed, "partial child listing must never execute the transaction")
				storedCluster, err := clusters.Get(ctx, cluster.ID.Name)
				require.NoError(t, err)
				require.Equal(t, cluster, storedCluster)
				storedNodePool, err := clusters.NodePools(cluster.ID.Name).Get(ctx, nodePool.ID.Name)
				require.NoError(t, err)
				require.Equal(t, nodePool, storedNodePool)
				storedExternalAuth, err := clusters.ExternalAuth(cluster.ID.Name).Get(ctx, externalAuth.ID.Name)
				require.NoError(t, err)
				require.Equal(t, externalAuth, storedExternalAuth)
				operations, err := db.Operations(cluster.ID.SubscriptionID).List(ctx, nil)
				require.NoError(t, err)
				for _, operation := range operations.Items(ctx) {
					t.Errorf("unexpected committed operation %s", operation.ResourceID)
				}
				require.NoError(t, operations.GetError())
			})
		}
	}
}
