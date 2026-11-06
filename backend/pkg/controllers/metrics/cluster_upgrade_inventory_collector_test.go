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

package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestClusterUpgradeInventorySurvivesCoalescedSPCDeletion(t *testing.T) {
	registry := prometheus.NewRegistry()
	parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	parent.CosmosMetadata.ResourceID = parent.ID
	parent.ServiceProviderProperties.ClusterServiceUninstallingObserved = true
	parents := cache.NewStore(resourceIDStoreKeyForObject)
	require.NoError(t, parents.Add(parent))
	registry.MustRegister(newClusterUpgradeInventoryCollector(parents, func() bool { return true }, "original"))

	handler := newTestClusterVersionMetricsHandler(t, registry, nil)
	spc := newTestServiceProviderCluster(t, parent, "4.20.43", nil)
	spc.Status.Conditions = []metav1.Condition{{
		Type: coreapi.ControlPlaneUpgradeMonitoringEnabled, Status: metav1.ConditionTrue,
	}}
	handler.Sync(context.Background(), spc)
	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(spc))
	key, err := resourceIDStoreKeyForObject(spc)
	require.NoError(t, err)
	controller := &Controller[*coreapi.ServiceProviderCluster]{indexer: indexer, handler: handler}

	// The disable update and deletion arrive before the queued key is processed.
	spc.Status.Conditions[0].Status = metav1.ConditionFalse
	require.NoError(t, indexer.Update(spc))
	require.NoError(t, indexer.Delete(spc))
	require.NoError(t, controller.syncResource(context.Background(), key))
	count, err := testutil.GatherAndCount(registry, "backend_cluster_upgrade_monitoring_enabled")
	require.NoError(t, err)
	require.Zero(t, count, "SPC metrics can be gone without losing the primary deletion evidence")

	expected := `# HELP backend_cluster_upgrade_inventory_size Number of primary clusters in a complete cache snapshot. Absent before initial cache synchronization.
# TYPE backend_cluster_upgrade_inventory_size gauge
backend_cluster_upgrade_inventory_size{backend_process="original"} 1
# HELP backend_cluster_upgrade_inventory_info Primary cluster inventory. Value is 1; deletion_confirmed records RP deletion or a durable CS uninstallation observation.
# TYPE backend_cluster_upgrade_inventory_info gauge
`
	expected += fmt.Sprintf("backend_cluster_upgrade_inventory_info{backend_process=\"original\",deletion_confirmed=\"true\",resource_id=\"%s\",subscription_id=\"%s\"} 1\n",
		resourceIDMetricLabel(parent.ID), subscriptionIDMetricLabel(parent.ID))
	require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(expected),
		"backend_cluster_upgrade_inventory_size", "backend_cluster_upgrade_inventory_info"))

	// Restart after all objects are removed: an initially unsynced cache is
	// unknown, while a successfully synced empty inventory is authoritative.
	require.NoError(t, parents.Delete(parent))
	restarted := prometheus.NewRegistry()
	synced := false
	restarted.MustRegister(newClusterUpgradeInventoryCollector(parents, func() bool { return synced }, "restarted"))
	count, err = testutil.GatherAndCount(restarted)
	require.NoError(t, err)
	require.Zero(t, count)
	synced = true
	expected = `# HELP backend_cluster_upgrade_inventory_size Number of primary clusters in a complete cache snapshot. Absent before initial cache synchronization.
# TYPE backend_cluster_upgrade_inventory_size gauge
backend_cluster_upgrade_inventory_size{backend_process="restarted"} 0
`
	require.NoError(t, testutil.GatherAndCompare(restarted, strings.NewReader(expected)))
}

func TestClusterUpgradeInventoryReadsDeletionWithoutMetricsQueue(t *testing.T) {
	parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	parent.CosmosMetadata.ResourceID = parent.ID
	parents := cache.NewStore(resourceIDStoreKeyForObject)
	require.NoError(t, parents.Add(parent))
	collector := newClusterUpgradeInventoryCollector(parents, func() bool { return true }, "snapshot")
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	parent = parent.DeepCopy()
	parent.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{}
	require.NoError(t, parents.Update(parent))
	metrics, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range metrics {
		if family.GetName() == "backend_cluster_upgrade_inventory_info" {
			for _, label := range family.Metric[0].Label {
				if label.GetName() == "deletion_confirmed" {
					require.Equal(t, "true", label.GetValue())
					return
				}
			}
		}
	}
	t.Fatal("missing primary lifecycle observation")
}
