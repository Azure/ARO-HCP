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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

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
	logLimiter     *rate.Limiter
}

func newClusterUpgradeInventoryCollector(store cache.Store, hasSynced cache.InformerSynced, processID string) *clusterUpgradeInventoryCollector {
	labels := prometheus.Labels{"backend_process": processID}
	return &clusterUpgradeInventoryCollector{
		store: store, hasSynced: hasSynced,
		logLimiter: rate.NewLimiter(rate.Every(5*time.Minute), 1),
		size: prometheus.NewDesc(
			"backend_cluster_upgrade_inventory_size",
			"Expected primary cluster inventory rows, including invalid cache entries. Absent before initial cache synchronization.",
			nil, labels,
		),
		info: prometheus.NewDesc(
			"backend_cluster_upgrade_inventory_info",
			"Primary cluster inventory. Value is 1 when RP or CS deletion or CS uninstallation was observed, otherwise 0.",
			[]string{"resource_id", "subscription_id"}, labels,
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
			continue
		}
		clusters = append(clusters, cluster)
	}
	if invalidObjects > 0 && c.logLimiter.Allow() {
		utils.DefaultLogger().Error(
			utils.TrackError(fmt.Errorf("%d invalid primary cluster inventory entries", invalidObjects)),
			"Unable to export all primary cluster inventory rows",
		)
	}
	// Keep invalid entries in the expected size so a valid subset cannot prove absence.
	ch <- prometheus.MustNewConstMetric(c.size, prometheus.GaugeValue, float64(len(objects)))
	ch <- prometheus.MustNewConstMetric(c.invalidObjects, prometheus.GaugeValue, float64(invalidObjects))
	for _, cluster := range clusters {
		properties := cluster.ServiceProviderProperties
		excluded := float64(0)
		if properties.DeletionTimestamp != nil || properties.ClusterServiceDeletionTimestamp != nil || properties.ClusterServiceUninstallingObserved {
			excluded = 1
		}
		ch <- prometheus.MustNewConstMetric(c.info, prometheus.GaugeValue, excluded,
			resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID))
	}
}
