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

package controllerutils

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
	syncFunc, _ := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateProvisioning, &now)
	syncFunc(context.Background(), cluster)

	resourceID := strings.ToLower(cluster.ID.String())
	hash := ResourceIDHash(resourceID)

	expectedState := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id_hash="%s"} 1
`, hash)
	err := testutil.GatherAndCompare(reg, strings.NewReader(expectedState), "backend_cluster_provision_state")
	require.NoError(t, err)

	expectedTime := fmt.Sprintf(`# HELP backend_cluster_created_time_seconds Unix timestamp when the cluster was created.
# TYPE backend_cluster_created_time_seconds gauge
backend_cluster_created_time_seconds{resource_id_hash="%s"} %d
`, hash, now.Unix())
	err = testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_cluster_created_time_seconds")
	require.NoError(t, err)
}

func TestClusterMetricsHandler_PhaseTransitionDeletesOldSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, _ := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, &now)
	syncFunc(context.Background(), cluster)

	cluster.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
	syncFunc(context.Background(), cluster)

	resourceID := strings.ToLower(cluster.ID.String())
	hash := ResourceIDHash(resourceID)
	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id_hash="%s"} 1
`, hash)

	err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_provision_state")
	require.NoError(t, err)
}

func TestClusterMetricsHandler_NilCreatedAt(t *testing.T) {
	reg := prometheus.NewRegistry()
	syncFunc, _ := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateAccepted, nil)
	syncFunc(context.Background(), cluster)

	resourceID := strings.ToLower(cluster.ID.String())
	hash := ResourceIDHash(resourceID)

	// provisionState should exist.
	expectedState := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="accepted",resource_id_hash="%s"} 1
`, hash)
	err := testutil.GatherAndCompare(reg, strings.NewReader(expectedState), "backend_cluster_provision_state")
	require.NoError(t, err)

	// createdTime should have no samples.
	require.Equal(t, 0, testutil.CollectAndCount(prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nonexistent_metric",
	}, []string{"x"})), "sanity check")
	err = testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_created_time_seconds")
	require.NoError(t, err, "should not emit created_time when CreatedAt is nil")
}

func TestClusterMetricsHandler_DeleteCleansUpAllGauges(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, deleteFunc := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)
	syncFunc(context.Background(), cluster)

	resourceID := strings.ToLower(cluster.ID.String())
	deleteFunc(resourceID)

	err := testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_provision_state", "backend_cluster_created_time_seconds")
	require.NoError(t, err, "expected no samples after delete")
}

func TestNodePoolMetricsHandler_SetsMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, _ := NewNodePoolMetricsHandler(reg)

	np := &api.HCPOpenShiftClusterNodePool{
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

	syncFunc(context.Background(), np)

	resourceID := strings.ToLower(np.ID.String())
	hash := ResourceIDHash(resourceID)
	expected := fmt.Sprintf(`# HELP backend_nodepool_provision_state Current provisioning state of the node pool (value is always 1).
# TYPE backend_nodepool_provision_state gauge
backend_nodepool_provision_state{phase="succeeded",resource_id_hash="%s"} 1
`, hash)
	err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_nodepool_provision_state")
	require.NoError(t, err)

	expectedTime := fmt.Sprintf(`# HELP backend_nodepool_created_time_seconds Unix timestamp when the node pool was created.
# TYPE backend_nodepool_created_time_seconds gauge
backend_nodepool_created_time_seconds{resource_id_hash="%s"} %d
`, hash, now.Unix())
	err = testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_nodepool_created_time_seconds")
	require.NoError(t, err)
}

func TestExternalAuthMetricsHandler_SetsMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, _ := NewExternalAuthMetricsHandler(reg)

	ea := &api.HCPOpenShiftClusterExternalAuth{
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

	syncFunc(context.Background(), ea)

	resourceID := strings.ToLower(ea.ID.String())
	hash := ResourceIDHash(resourceID)
	expected := fmt.Sprintf(`# HELP backend_externalauth_provision_state Current provisioning state of the external auth (value is always 1).
# TYPE backend_externalauth_provision_state gauge
backend_externalauth_provision_state{phase="accepted",resource_id_hash="%s"} 1
`, hash)
	err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_externalauth_provision_state")
	require.NoError(t, err)

	expectedTime := fmt.Sprintf(`# HELP backend_externalauth_created_time_seconds Unix timestamp when the external auth was created.
# TYPE backend_externalauth_created_time_seconds gauge
backend_externalauth_created_time_seconds{resource_id_hash="%s"} %d
`, hash, now.Unix())
	err = testutil.GatherAndCompare(reg, strings.NewReader(expectedTime), "backend_externalauth_created_time_seconds")
	require.NoError(t, err)
}

func TestSyncResource_SetsMetricsFromIndexer(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, deleteFunc := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	c := &ResourceMetricsController[*api.HCPOpenShiftCluster]{
		name:          "TestMetrics",
		indexer:       indexer,
		syncMetrics:   syncFunc,
		deleteMetrics: deleteFunc,
	}

	key, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	err = c.syncResource(context.Background(), key)
	require.NoError(t, err)

	resourceID := strings.ToLower(cluster.ID.String())
	hash := ResourceIDHash(resourceID)
	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the cluster (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="succeeded",resource_id_hash="%s"} 1
`, hash)
	err = testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_provision_state")
	require.NoError(t, err)
}

func TestSyncResource_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	syncFunc, deleteFunc := NewClusterMetricsHandler(reg)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, &now)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	c := &ResourceMetricsController[*api.HCPOpenShiftCluster]{
		name:          "TestMetrics",
		indexer:       indexer,
		syncMetrics:   syncFunc,
		deleteMetrics: deleteFunc,
	}

	key, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	err = c.syncResource(context.Background(), key)
	require.NoError(t, err)

	require.NoError(t, indexer.Delete(cluster))

	err = c.syncResource(context.Background(), key)
	require.NoError(t, err)

	err = testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_provision_state", "backend_cluster_created_time_seconds")
	require.NoError(t, err, "expected no samples after deletion")
}
