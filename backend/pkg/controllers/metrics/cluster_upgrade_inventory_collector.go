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
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterUpgradeInventoryCollector struct {
	store          cache.Store
	hasSynced      cache.InformerSynced
	size           *prometheus.Desc
	info           *prometheus.Desc
	invalidObjects *prometheus.Desc
}

func newClusterUpgradeInventoryCollector(store cache.Store, hasSynced cache.InformerSynced, processID string) *clusterUpgradeInventoryCollector {
	labels := prometheus.Labels{"backend_process": processID}
	return &clusterUpgradeInventoryCollector{
		store: store, hasSynced: hasSynced,
		size: prometheus.NewDesc(
			"backend_cluster_upgrade_inventory_size",
			"Expected primary cluster inventory rows, including invalid cache entries. Absent before initial cache synchronization.",
			nil, labels,
		),
		info: prometheus.NewDesc(
			"backend_cluster_upgrade_inventory_info",
			"Primary cluster inventory. Value is 1; deletion_confirmed records RP deletion or a durable CS uninstallation observation.",
			[]string{"resource_id", "subscription_id", "deletion_confirmed"}, labels,
		),
		invalidObjects: prometheus.NewDesc(
			"backend_cluster_upgrade_inventory_invalid_objects",
			"Number of invalid entries in the primary cluster inventory snapshot.",
			nil, labels,
		),
	}
}

func (c *clusterUpgradeInventoryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.size
	ch <- c.info
	ch <- c.invalidObjects
}

func (c *clusterUpgradeInventoryCollector) Collect(ch chan<- prometheus.Metric) {
	if !c.hasSynced() {
		return
	}
	// Read one store snapshot, not the metrics workqueue: a disable update may
	// coalesce with object deletion before a worker ever observes it.
	objects := c.store.List()
	clusters := make([]*coreapi.Cluster, 0, len(objects))
	invalidObjects := 0
	for _, obj := range objects {
		cluster, ok := obj.(*coreapi.Cluster)
		if !ok || cluster == nil || cluster.ID == nil {
			invalidObjects++
			utils.DefaultLogger().Error(
				utils.TrackError(fmt.Errorf("invalid primary cluster inventory object %T", obj)),
				"Unable to export a primary cluster inventory row",
			)
			continue
		}
		clusters = append(clusters, cluster)
	}
	// Keep invalid entries in the expected size so a valid subset cannot prove absence.
	ch <- prometheus.MustNewConstMetric(c.size, prometheus.GaugeValue, float64(len(objects)))
	ch <- prometheus.MustNewConstMetric(c.invalidObjects, prometheus.GaugeValue, float64(invalidObjects))
	for _, cluster := range clusters {
		properties := cluster.ServiceProviderProperties
		excluded := properties.DeletionTimestamp != nil || properties.ClusterServiceUninstallingObserved
		ch <- prometheus.MustNewConstMetric(c.info, prometheus.GaugeValue, 1,
			resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID), strconv.FormatBool(excluded))
	}
}
