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

package metricscontrollers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func newTestCluster(t *testing.T, name string, state arm.ProvisioningState, createdAt *time.Time) *api.HCPOpenShiftCluster {
	t.Helper()

	var systemData *arm.SystemData
	if createdAt != nil {
		systemData = &arm.SystemData{CreatedAt: createdAt}
	}

	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name)),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name)),
				SystemData: systemData,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: state,
		},
	}
}

func TestClusterMetricsHandler_SetsProvisionStateAndCreatedTime(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateProvisioning, &now)
	handler.Sync(context.Background(), cluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expectedState := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedState), "backend_cluster_provision_state"))

	expectedTime := fmt.Sprintf(`# HELP backend_cluster_created_time_seconds Unix timestamp when the cluster was created.
# TYPE backend_cluster_created_time_seconds gauge
backend_cluster_created_time_seconds{resource_id="%s",subscription_id="%s"} %d
`, resourceID, subscriptionID, now.Unix())
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_cluster_created_time_seconds"))
}

func TestClusterMetricsHandler_PhaseTransitionDeletesOldSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, &now)
	handler.Sync(context.Background(), cluster)

	cluster.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
	handler.Sync(context.Background(), cluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_provision_state"))
}

func TestClusterMetricsHandler_NilCreatedAt(t *testing.T) {
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)
	handler.Sync(context.Background(), cluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expectedState := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="accepted",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedState), "backend_cluster_provision_state"))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_created_time_seconds"))
}

func TestClusterMetricsHandler_DeleteCleansUpAllGauges(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)
	handler.Sync(context.Background(), cluster)
	handler.Delete(strings.ToLower(cluster.ID.String()))

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_provision_state", "backend_cluster_created_time_seconds"))
}

func TestNodePoolMetricsHandler_SetsMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewNodePoolMetricsHandler(reg)

	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1")),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
		},
	}

	handler.Sync(context.Background(), nodePool)

	resourceID := resourceIDMetricLabel(nodePool.ID)
	subscriptionID := subscriptionIDMetricLabel(nodePool.ID)
	expected := fmt.Sprintf(`# HELP backend_nodepool_provision_state Current provisioning state of the node pool (value is always 1).
# TYPE backend_nodepool_provision_state gauge
backend_nodepool_provision_state{phase="succeeded",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_nodepool_provision_state"))

	expectedTime := fmt.Sprintf(`# HELP backend_nodepool_created_time_seconds Unix timestamp when the node pool was created.
# TYPE backend_nodepool_created_time_seconds gauge
backend_nodepool_created_time_seconds{resource_id="%s",subscription_id="%s"} %d
`, resourceID, subscriptionID, now.Unix())
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_nodepool_created_time_seconds"))
}

func TestExternalAuthMetricsHandler_SetsMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewExternalAuthMetricsHandler(reg)

	externalAuth := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1")),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
		},
	}

	handler.Sync(context.Background(), externalAuth)

	resourceID := resourceIDMetricLabel(externalAuth.ID)
	subscriptionID := subscriptionIDMetricLabel(externalAuth.ID)
	expected := fmt.Sprintf(`# HELP backend_externalauth_provision_state Current provisioning state of the external auth (value is always 1).
# TYPE backend_externalauth_provision_state gauge
backend_externalauth_provision_state{phase="accepted",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_externalauth_provision_state"))

	expectedTime := fmt.Sprintf(`# HELP backend_externalauth_created_time_seconds Unix timestamp when the external auth was created.
# TYPE backend_externalauth_created_time_seconds gauge
backend_externalauth_created_time_seconds{resource_id="%s",subscription_id="%s"} %d
`, resourceID, subscriptionID, now.Unix())
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_externalauth_created_time_seconds"))
}

func TestResourceControllerSyncResource_SetsMetricsFromIndexer(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	controller := &Controller[*api.HCPOpenShiftCluster]{
		name:    "TestMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(cluster)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="succeeded",resource_id="%s",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_provision_state"))
}

func TestResourceControllerSyncResource_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	handler := NewClusterMetricsHandler(reg)
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	controller := &Controller[*api.HCPOpenShiftCluster]{
		name:    "TestMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(cluster)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))
	require.NoError(t, indexer.Delete(cluster))
	require.NoError(t, controller.syncResource(context.Background(), key))

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_provision_state", "backend_cluster_created_time_seconds"))
}

func TestResourceIDStoreKeyForObject_HandlesTombstone(t *testing.T) {
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)
	key, err := resourceIDStoreKeyForObject(cache.DeletedFinalStateUnknown{Obj: cluster})
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(cluster.ID.String()), key)
}

func TestResourceIDStoreKeyForObject_HandlesKeyOnlyTombstone(t *testing.T) {
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)
	key, err := resourceIDStoreKeyForObject(cache.DeletedFinalStateUnknown{Key: strings.ToUpper(cluster.ID.String())})
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(cluster.ID.String()), key)
}

func TestResourceIDStoreKeyForObject_HandlesNestedTombstones(t *testing.T) {
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)
	key, err := resourceIDStoreKeyForObject(cache.DeletedFinalStateUnknown{
		Obj: &cache.DeletedFinalStateUnknown{Obj: cluster},
	})
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(cluster.ID.String()), key)
}

func TestResourceIDStoreKeyForObject_RejectsEmptyTombstone(t *testing.T) {
	_, err := resourceIDStoreKeyForObject(cache.DeletedFinalStateUnknown{})
	require.ErrorContains(t, err, "tombstone missing key and object")
}

func TestResourceIDStoreKeyForObject_RejectsCyclicTombstones(t *testing.T) {
	tombstone := &cache.DeletedFinalStateUnknown{}
	tombstone.Obj = tombstone

	_, err := resourceIDStoreKeyForObject(tombstone)
	require.ErrorContains(t, err, "max unwrap depth")
}

func TestResourceIDStoreKeyForObject_MatchesMetaNamespaceKeyFuncForCluster(t *testing.T) {
	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)

	got, err := resourceIDStoreKeyForObject(cluster)
	require.NoError(t, err)

	expected, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	require.Equal(t, expected, got)
}
