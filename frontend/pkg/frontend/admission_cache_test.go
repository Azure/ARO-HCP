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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Admission must not silently reintroduce live inventory lists.
type noInventoryListsDB struct {
	corecosmosstorage.ResourcesDBClient
}

func (d noInventoryListsDB) HCPClusters(subscriptionID, resourceGroupName string) corecosmosstorage.HCPClusterCRUD {
	return noClusterListCRUD{d.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)}
}

type noClusterListCRUD struct {
	corecosmosstorage.HCPClusterCRUD
}

func (c noClusterListCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Cluster], error) {
	return nil, fmt.Errorf("unexpected live cluster inventory list")
}

func (c noClusterListCRUD) NodePools(clusterName string) corecosmosstorage.NodePoolsCRUD {
	return noNodePoolListCRUD{c.HCPClusterCRUD.NodePools(clusterName)}
}

type noNodePoolListCRUD struct {
	corecosmosstorage.NodePoolsCRUD
}

func (c noNodePoolListCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.NodePool], error) {
	return nil, fmt.Errorf("unexpected live node pool inventory list")
}

func TestClusterCreateAdmissionCachedInventory(t *testing.T) {
	cluster := coreapitesting.MinimumValidClusterTestCase()
	otherGroup := coreapi.NewDefaultCluster(metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(cluster.ID.String(), cluster.ID.ResourceGroupName, "other-group", 1))), coreapitesting.TestLocation)
	otherSubscription := coreapi.NewDefaultCluster(metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(cluster.ID.String(), cluster.ID.SubscriptionID, "11111111-2222-4333-8444-555555555555", 1))), coreapitesting.TestLocation)
	nodePool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(otherGroup.ID.String()+"/nodePools/pool")), coreapitesting.TestLocation)
	orphan := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String()+"-absent/nodePools/orphan")), coreapitesting.TestLocation)
	foreignPool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(otherSubscription.ID.String()+"/nodePools/foreign")), coreapitesting.TestLocation)
	for _, c := range []*coreapi.Cluster{cluster, otherGroup, otherSubscription} {
		c.SetResourceID(c.ID)
		c.SetPartitionKey(c.ID.SubscriptionID)
	}
	for _, p := range []*coreapi.NodePool{nodePool, orphan, foreignPool} {
		p.SetResourceID(p.ID)
		p.SetPartitionKey(p.ID.SubscriptionID)
	}
	f := NewTestFrontend(t)
	// The cache alone contains these resources, including an orphan that must not
	// be considered without its parent cluster in the collision inventory.
	f.clusterLister = corelisters.NewClusterLister(newTestClusterInformer(t, cluster, otherGroup, otherSubscription).GetIndexer())
	f.nodePoolLister = corelisters.NewNodePoolLister(newTestNodePoolInformer(t, nodePool, orphan, foreignPool).GetIndexer())
	f.resourcesDBClient = noInventoryListsDB{f.resourcesDBClient}
	subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil)
	ctx, err := f.newClusterAdmissionContext(t.Context(), operation.Operation{Type: operation.Create}, subscription, cluster, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []*coreapi.Cluster{cluster, otherGroup}, ctx.SubscriptionClusters)
	require.Equal(t, []*coreapi.NodePool{nodePool}, ctx.SubscriptionNodePools)
	beforeClusters := []*coreapi.Cluster{cluster.DeepCopy(), otherGroup.DeepCopy()}
	beforePools := []*coreapi.NodePool{nodePool.DeepCopy()}
	_ = admission.MutateCluster(t.Context(), ctx, operation.Operation{Type: operation.Create}, cluster.DeepCopy(), nil)
	_ = admission.AdmitCluster(t.Context(), ctx, operation.Operation{Type: operation.Create}, cluster.DeepCopy(), nil)
	require.ElementsMatch(t, beforeClusters, ctx.SubscriptionClusters, "admission must not mutate cached clusters")
	require.Equal(t, beforePools, ctx.SubscriptionNodePools, "admission must not mutate cached node pools")
}

func TestAdmissionProviderCache(t *testing.T) {
	for _, kind := range []string{"cluster update", "node pool create", "node pool update"} {
		for _, state := range []string{"present", "missing cluster", "missing node pool", "deleting node pool", "deleting node pool missing provider"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				f := NewTestFrontend(t)
				// Any DB access, including provider reads/writes or live-parent
				// verification, panics. All dependencies must come from the cache.
				f.resourcesDBClient = nil
				cluster := coreapitesting.MinimumValidClusterTestCase()
				pool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
				pool.SetResourceID(pool.ID)
				pool.SetPartitionKey(pool.ID.SubscriptionID)
				pool.Properties.Version.ID = "4.19.0"
				if state == "deleting node pool" || state == "deleting node pool missing provider" {
					now := metav1.Now()
					pool.ServiceProviderProperties.DeletionTimestamp = &now
				}
				poolInformer, _ := f.informers.NodePools()
				require.NoError(t, poolInformer.GetStore().Add(pool.DeepCopy()))
				serviceProviderCluster := newTestServiceProviderCluster(cluster.ID)
				serviceProviderNodePool := newTestServiceProviderNodePool(pool.ID)
				if state != "missing cluster" {
					informer, _ := f.informers.ServiceProviderClusters()
					require.NoError(t, informer.GetStore().Add(serviceProviderCluster.DeepCopy()))
				}
				if state == "present" || state == "missing cluster" || state == "deleting node pool" {
					informer, _ := f.informers.ServiceProviderNodePools()
					require.NoError(t, informer.GetStore().Add(serviceProviderNodePool.DeepCopy()))
				}
				subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil)
				var err error
				if kind == "cluster update" {
					var ac *admission.ClusterAdmissionContext
					ac, err = f.newClusterAdmissionContext(t.Context(), operation.Operation{Type: operation.Update}, subscription, cluster, cluster.ID)
					if err == nil {
						require.Equal(t, serviceProviderCluster, ac.ServiceProviderCluster)
						require.Equal(t, []admission.ClusterAdmissionNodePool{{NodePool: pool, ServiceProviderNodePool: serviceProviderNodePool}}, ac.ClusterNodePools)
						_ = admission.AdmitCluster(t.Context(), ac, operation.Operation{Type: operation.Update}, cluster.DeepCopy(), cluster.DeepCopy())
					}
				} else {
					op := operation.Operation{Type: operation.Update}
					if kind == "node pool create" {
						op.Type = operation.Create
					}
					var ac *admission.NodePoolAdmissionContext
					ac, err = f.newNodePoolAdmissionContext(t.Context(), op, subscription, pool, cluster)
					if err == nil {
						require.Equal(t, serviceProviderCluster, ac.ServiceProviderCluster)
						if op.Type == operation.Update {
							require.Equal(t, serviceProviderNodePool, ac.ServiceProviderNodePool)
						} else {
							require.Nil(t, ac.ServiceProviderNodePool)
						}
						_ = admission.AdmitNodePool(t.Context(), ac, op, pool.DeepCopy(), pool.DeepCopy())
					}
				}
				wantError := state == "missing cluster" || ((state == "missing node pool" || state == "deleting node pool missing provider") && kind != "node pool create")
				if wantError {
					require.ErrorContains(t, err, "cannot load service provider")
					require.ErrorContains(t, err, cluster.ID.String())
					recorder := httptest.NewRecorder()
					request := httptest.NewRequest("PUT", pool.ID.String(), nil).WithContext(utils.ContextWithResourceID(t.Context(), pool.ID))
					errorutils.ReportError(func(http.ResponseWriter, *http.Request) error { return err })(recorder, request)
					require.Equal(t, http.StatusInternalServerError, recorder.Code, "missing provider state is not a missing ARM target")
				} else {
					require.NoError(t, err)
				}
				cachedPools, listErr := f.nodePoolLister.List(t.Context())
				require.NoError(t, listErr)
				require.Equal(t, []*coreapi.NodePool{pool}, cachedPools, "admission must not mutate cached state")
			})
		}
	}
}

func TestClusterUpdateAdmissionDeletingNodePoolVersionSkew(t *testing.T) {
	for _, poolVersion := range []string{"4.19.0", "4.18.0"} {
		t.Run(poolVersion, func(t *testing.T) {
			f := NewTestFrontend(t)
			f.resourcesDBClient = nil
			cluster := coreapitesting.MinimumValidClusterTestCase()
			cluster.CustomerProperties.Version.ID = "4.20"
			desired := cluster.DeepCopy()
			desired.CustomerProperties.Version.ID = "4.21"
			pool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
			pool.SetResourceID(pool.ID)
			pool.SetPartitionKey(pool.ID.SubscriptionID)
			pool.Properties.Version.ID = poolVersion
			now := metav1.Now()
			pool.ServiceProviderProperties.DeletionTimestamp = &now
			poolInformer, _ := f.informers.NodePools()
			require.NoError(t, poolInformer.GetStore().Add(pool.DeepCopy()))
			clusterInformer, _ := f.informers.ServiceProviderClusters()
			require.NoError(t, clusterInformer.GetStore().Add(newTestServiceProviderCluster(cluster.ID)))
			providerPoolInformer, _ := f.informers.ServiceProviderNodePools()
			require.NoError(t, providerPoolInformer.GetStore().Add(newTestServiceProviderNodePool(pool.ID)))
			subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil)
			op := operation.Operation{Type: operation.Update}
			ac, err := f.newClusterAdmissionContext(t.Context(), op, subscription, desired, cluster.ID)
			require.NoError(t, err)
			validationErrs := admission.AdmitCluster(t.Context(), ac, op, desired, cluster)
			if poolVersion == "4.19.0" {
				require.Empty(t, validationErrs)
			} else {
				require.Len(t, validationErrs, 1)
				require.Equal(t, "properties.version.id", validationErrs[0].Field)
				require.Contains(t, validationErrs[0].Detail, "must not be more than two minor versions ahead of node pool")
			}
		})
	}
}

func newTestServiceProviderCluster(clusterID *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	return &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(clusterID.String() + "/serviceProviderClusters/default")),
		PartitionKey: strings.ToLower(clusterID.SubscriptionID),
	}}
}

func newTestServiceProviderNodePool(poolID *azcorearm.ResourceID) *coreapi.ServiceProviderNodePool {
	return &coreapi.ServiceProviderNodePool{CosmosMetadata: coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(poolID.String() + "/serviceProviderNodePools/default")),
		PartitionKey: strings.ToLower(poolID.SubscriptionID),
	}}
}

type noProviderDB struct {
	corecosmosstorage.ResourcesDBClient
}

func (noProviderDB) ServiceProviderClusters(string, string, string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	panic("admission must not read or write provider clusters in the DB")
}

func (noProviderDB) ServiceProviderNodePools(string, string, string, string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool] {
	panic("admission must not read or write provider node pools in the DB")
}

func TestAdmissionProviderCacheHandlers(t *testing.T) {
	for _, kind := range []string{"cluster update", "node pool create", "node pool update"} {
		for _, missing := range []string{"none", "cluster", "node pool"} {
			t.Run(kind+"/missing "+missing, func(t *testing.T) {
				f := NewTestFrontend(t)
				db := f.resourcesDBClient
				f.resourcesDBClient = noProviderDB{db}
				cluster := coreapitesting.MinimumValidClusterTestCase()
				cluster.SetResourceID(cluster.ID)
				cluster.SetPartitionKey(cluster.ID.SubscriptionID)
				cluster.ServiceProviderProperties.ClusterServiceID = cluster.ServiceProviderProperties.PendingClusterServiceID
				_, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(t.Context(), cluster, nil)
				require.NoError(t, err)
				_, err = db.Subscriptions().Create(t.Context(), newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil), nil)
				require.NoError(t, err)
				pool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
				pool.SetResourceID(pool.ID)
				pool.SetPartitionKey(pool.ID.SubscriptionID)
				poolInformer, _ := f.informers.NodePools()
				require.NoError(t, poolInformer.GetStore().Add(pool.DeepCopy()))
				if missing != "cluster" {
					informer, _ := f.informers.ServiceProviderClusters()
					require.NoError(t, informer.GetStore().Add(newTestServiceProviderCluster(cluster.ID)))
				}
				if missing != "node pool" {
					informer, _ := f.informers.ServiceProviderNodePools()
					require.NoError(t, informer.GetStore().Add(newTestServiceProviderNodePool(pool.ID)))
				}
				version, ok := f.apiRegistry.Lookup(coreapitesting.TestAPIVersion)
				require.True(t, ok)
				ctx := ContextWithVersion(t.Context(), version)
				ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
				ctx = ContextWithSystemData(ctx, cluster.SystemData)
				ctx = utils.ContextWithResourceID(ctx, pool.ID)
				ctx = ContextWithBody(ctx, []byte(`{"properties":{"platform":{"osDisk":{"sizeGiB":-1}}}}`))
				request := httptest.NewRequest("PUT", pool.ID.String(), nil).WithContext(ctx)
				recorder := httptest.NewRecorder()
				handler := func(w http.ResponseWriter, r *http.Request) error {
					switch kind {
					case "cluster update":
						desired := cluster.DeepCopy()
						desired.CustomerProperties.Version.ID = "invalid"
						return f.updateHCPClusterInCosmos(ctx, w, r, http.StatusAccepted, desired, cluster)
					case "node pool update":
						desired := pool.DeepCopy()
						invalidSize := int32(-1)
						desired.Properties.Platform.OSDisk.SizeGiB = &invalidSize
						return f.updateNodePoolInCosmos(ctx, w, r, http.StatusAccepted, desired, pool)
					default:
						return f.createNodePool(w, r)
					}
				}
				errorutils.ReportError(handler)(recorder, request)
				if missing == "cluster" || (missing == "node pool" && kind != "node pool create") {
					require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
				} else {
					// Invalid user input stops after admission, before an operation
					// transaction, without requiring any provider DB access.
					require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

func TestDeleteNodePoolCachedLastPoolInventory(t *testing.T) {
	f := NewTestFrontend(t)
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.SetResourceID(cluster.ID)
	cluster.SetPartitionKey(cluster.ID.SubscriptionID)
	_, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(t.Context(), cluster, nil)
	require.NoError(t, err)
	pool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
	pool.SetResourceID(pool.ID)
	pool.SetPartitionKey(pool.ID.SubscriptionID)
	pool.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
	_, err = db.HCPClusters(pool.ID.SubscriptionID, pool.ID.ResourceGroupName).NodePools(pool.ID.Parent.Name).Create(t.Context(), pool, nil)
	require.NoError(t, err)
	f.resourcesDBClient = noInventoryListsDB{db}
	f.nodePoolLister = corelisters.NewNodePoolLister(newTestNodePoolInformer(t, pool).GetIndexer())
	request := httptest.NewRequest("DELETE", pool.ID.String(), nil)
	request = request.WithContext(utils.ContextWithResourceID(t.Context(), pool.ID))
	err = f.DeleteNodePool(httptest.NewRecorder(), request)
	require.ErrorContains(t, err, "The last node pool can not be deleted")
}
