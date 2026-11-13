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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func TestClusterUpgradeInventorySurvivesCoalescedSPCDeletion(t *testing.T) {
	registry := prometheus.NewRegistry()
	parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	parent.ResourceID = parent.ID
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

	expected := `# HELP backend_cluster_upgrade_inventory_size Expected primary cluster inventory rows, including invalid cache entries. Absent before initial cache synchronization.
# TYPE backend_cluster_upgrade_inventory_size gauge
backend_cluster_upgrade_inventory_size{backend_process="original"} 1
# HELP backend_cluster_upgrade_inventory_info Primary cluster inventory. Value is 1 when RP or CS deletion or CS uninstallation was observed, otherwise 0.
# TYPE backend_cluster_upgrade_inventory_info gauge
`
	expected += fmt.Sprintf("backend_cluster_upgrade_inventory_info{backend_process=\"original\",resource_id=\"%s\",subscription_id=\"%s\"} 1\n",
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
	expected = `# HELP backend_cluster_upgrade_inventory_size Expected primary cluster inventory rows, including invalid cache entries. Absent before initial cache synchronization.
# TYPE backend_cluster_upgrade_inventory_size gauge
backend_cluster_upgrade_inventory_size{backend_process="restarted"} 0
# HELP backend_cluster_upgrade_inventory_invalid_objects Number of invalid entries in the primary cluster inventory snapshot.
# TYPE backend_cluster_upgrade_inventory_invalid_objects gauge
backend_cluster_upgrade_inventory_invalid_objects{backend_process="restarted"} 0
`
	require.NoError(t, testutil.GatherAndCompare(restarted, strings.NewReader(expected)))
}

func TestClusterUpgradeInventoryReadsDeletionWithoutMetricsQueue(t *testing.T) {
	parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	parent.ResourceID = parent.ID
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
			require.Equal(t, float64(1), family.Metric[0].GetGauge().GetValue())
			for _, label := range family.Metric[0].Label {
				require.NotEqual(t, "deletion_confirmed", label.GetName())
			}
			return
		}
	}
	t.Fatal("missing primary lifecycle observation")
}

func TestUpgradeMetricsShareEpochAndStableInventoryIdentity(t *testing.T) {
	parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	parents := cache.NewStore(resourceIDStoreKeyForObject)
	parent.ResourceID = parent.ID
	require.NoError(t, parents.Add(parent))
	registry := prometheus.NewRegistry()
	collector := newClusterUpgradeInventoryCollector(parents, func() bool { return true }, "shared-process")
	registry.MustRegister(collector)
	handler := NewClusterVersionMetricsHandler(registry, &kubeapplierlistertesting.SliceReadDesireLister{}, "shared-process")
	handler.Sync(context.Background(), newTestServiceProviderCluster(t, parent, "4.20.43", []coreapi.ServiceProviderClusterActiveVersion{{
		Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.CompletedUpdate,
	}}))

	var previousLabels string
	for _, deleting := range []bool{false, true} {
		if deleting {
			parent = parent.DeepCopy()
			parent.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{}
			require.NoError(t, parents.Update(parent))
		}
		families, err := registry.Gather()
		require.NoError(t, err)
		for _, family := range families {
			for _, metric := range family.Metric {
				processID := ""
				labels := strings.Builder{}
				for _, label := range metric.Label {
					if label.GetName() == "backend_process" {
						processID = label.GetValue()
					}
					fmt.Fprintf(&labels, "%s=%s;", label.GetName(), label.GetValue())
					require.NotEqual(t, "deletion_confirmed", label.GetName())
				}
				if family.GetName() == "backend_cluster_version_info" {
					require.Empty(t, processID, "legacy version metric labels must remain compatible")
				} else {
					require.Equal(t, "shared-process", processID)
				}
				if family.GetName() == "backend_cluster_upgrade_inventory_info" {
					want := float64(0)
					if deleting {
						want = 1
						require.Equal(t, previousLabels, labels.String(), "teardown changes the value, not the series identity")
					} else {
						previousLabels = labels.String()
					}
					require.Equal(t, want, metric.GetGauge().GetValue())
				}
			}
		}
	}
}

func TestClusterUpgradeInventoryMalformedEntriesDoNotFailScrape(t *testing.T) {
	for _, tc := range []struct {
		name         string
		invalid      interface{}
		includeValid bool
	}{
		{"unexpected object", &struct{}{}, true},
		{"missing resource ID", &coreapi.Cluster{}, true},
		{"only malformed objects", &struct{}{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
			parents := cache.NewStore(func(obj interface{}) (string, error) {
				return fmt.Sprintf("%T:%p", obj, obj), nil
			})
			if tc.includeValid {
				require.NoError(t, parents.Add(parent))
			}
			require.NoError(t, parents.Add(tc.invalid))
			registry := prometheus.NewRegistry()
			collector := newClusterUpgradeInventoryCollector(parents, func() bool { return true }, "snapshot")
			registry.MustRegister(collector)
			unrelated := prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "unrelated_test_metric", Help: "Unrelated metrics must remain available.",
			})
			unrelated.Set(42)
			registry.MustRegister(unrelated)

			for _, invalidPresent := range []bool{true, false} {
				if !invalidPresent {
					require.NoError(t, parents.Delete(tc.invalid))
				}
				response := httptest.NewRecorder()
				promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(
					response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				require.Equal(t, http.StatusOK, response.Code, "malformed inventory must not fail the entire scrape: %s", response.Body.String())
				require.Contains(t, response.Body.String(), "unrelated_test_metric 42")

				expectedSize, invalidCount := 0, 0
				if tc.includeValid {
					expectedSize++
				}
				if invalidPresent {
					expectedSize++
					invalidCount++
				}
				expected := fmt.Sprintf(`# HELP backend_cluster_upgrade_inventory_size Expected primary cluster inventory rows, including invalid cache entries. Absent before initial cache synchronization.
# TYPE backend_cluster_upgrade_inventory_size gauge
backend_cluster_upgrade_inventory_size{backend_process="snapshot"} %d
# HELP backend_cluster_upgrade_inventory_invalid_objects Number of invalid entries in the primary cluster inventory snapshot.
# TYPE backend_cluster_upgrade_inventory_invalid_objects gauge
backend_cluster_upgrade_inventory_invalid_objects{backend_process="snapshot"} %d
`, expectedSize, invalidCount)
				if tc.includeValid {
					expected += fmt.Sprintf(`# HELP backend_cluster_upgrade_inventory_info Primary cluster inventory. Value is 1 when RP or CS deletion or CS uninstallation was observed, otherwise 0.
# TYPE backend_cluster_upgrade_inventory_info gauge
backend_cluster_upgrade_inventory_info{backend_process="snapshot",resource_id="%s",subscription_id="%s"} 0
`, resourceIDMetricLabel(parent.ID), subscriptionIDMetricLabel(parent.ID))
				}
				require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(expected),
					"backend_cluster_upgrade_inventory_size", "backend_cluster_upgrade_inventory_info",
					"backend_cluster_upgrade_inventory_invalid_objects"))
				if invalidPresent {
					require.False(t, collector.logLimiter.Allow(), "repeated malformed scrapes must not produce another error log within the cooldown")
				}
			}
		})
	}
}
