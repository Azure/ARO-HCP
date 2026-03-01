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

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	hcpopenshiftclustersDesc = prometheus.NewDesc(
		"backend_hcpopenshiftclusters",
		"Number of clusters by provisioning state.",
		[]string{"provisioning_state"}, nil,
	)

	nodepoolsDesc = prometheus.NewDesc(
		"backend_nodepools",
		"Number of node pools by provisioning state.",
		[]string{"provisioning_state"}, nil,
	)

	externalauthsDesc = prometheus.NewDesc(
		"backend_externalauths",
		"Number of external auths by provisioning state.",
		[]string{"provisioning_state"}, nil,
	)

	activeOperationsDesc = prometheus.NewDesc(
		"backend_active_operations",
		"Number of active operations by provisioning state.",
		[]string{"provisioning_state"}, nil,
	)

	subscriptionsDesc = prometheus.NewDesc(
		"backend_subscriptions",
		"Number of subscriptions by state.",
		[]string{"state"}, nil,
	)

	// controller records are nested under the resource they act on (e.g.
	// under a ServiceProviderCluster). resource_type distinguishes controllers
	// that share the same name but operate on different parent resource types.
	controllerStatusConditionsDesc = prometheus.NewDesc(
		"backend_controller_status_conditions",
		"Count of controller instances by resource type, controller name, condition type, and condition status.",
		[]string{"resource_type", "controller_name", "condition", "status"}, nil,
	)
)

// CosmosMetricsCollector implements prometheus.Collector to expose
// Cosmos DB resource counts and controller degradation metrics.
// Metrics are computed on-demand at each Prometheus scrape.
//
// An alternative design would maintain running counts via informer
// event handlers (OnAdd/OnUpdate/OnDelete) instead of re-listing on
// every scrape. For the foreseeable future the number of resources
// does not justify this added complexity.
type CosmosMetricsCollector struct {
	backendInformers informers.BackendInformers
	logger           logr.Logger
}

// NewCosmosMetricsCollector creates a new CosmosMetricsCollector.
func NewCosmosMetricsCollector(backendInformers informers.BackendInformers, logger logr.Logger) *CosmosMetricsCollector {
	return &CosmosMetricsCollector{
		backendInformers: backendInformers,
		logger:           logger,
	}
}

// Describe sends the metric descriptors to the channel.
func (c *CosmosMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- hcpopenshiftclustersDesc
	ch <- nodepoolsDesc
	ch <- externalauthsDesc
	ch <- activeOperationsDesc
	ch <- subscriptionsDesc
	ch <- controllerStatusConditionsDesc
}

// Collect iterates lister caches and sends metric values to the channel.
// Each resource type is handled independently so that a failure in one
// lister does not prevent other metrics from being collected.
func (c *CosmosMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	// Collect cluster counts by provisioning state
	clusterInformer, clusterLister := c.backendInformers.Clusters()
	if clusterInformer.HasSynced() {
		if clusters, err := clusterLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list clusters for metrics")
		} else {
			counts := make(map[arm.ProvisioningState]int)
			for _, cluster := range clusters {
				counts[cluster.ServiceProviderProperties.ProvisioningState]++
			}
			for state, count := range counts {
				ch <- prometheus.MustNewConstMetric(hcpopenshiftclustersDesc, prometheus.GaugeValue, float64(count), string(state))
			}
		}
	}

	// Collect nodepool counts by provisioning state
	nodePoolInformer, nodePoolLister := c.backendInformers.NodePools()
	if nodePoolInformer.HasSynced() {
		if nodePools, err := nodePoolLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list node pools for metrics")
		} else {
			counts := make(map[arm.ProvisioningState]int)
			for _, np := range nodePools {
				counts[np.Properties.ProvisioningState]++
			}
			for state, count := range counts {
				ch <- prometheus.MustNewConstMetric(nodepoolsDesc, prometheus.GaugeValue, float64(count), string(state))
			}
		}
	}

	// Collect external auth counts by provisioning state
	externalAuthInformer, externalAuthLister := c.backendInformers.ExternalAuths()
	if externalAuthInformer.HasSynced() {
		if externalAuths, err := externalAuthLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list external auths for metrics")
		} else {
			counts := make(map[arm.ProvisioningState]int)
			for _, ea := range externalAuths {
				counts[ea.Properties.ProvisioningState]++
			}
			for state, count := range counts {
				ch <- prometheus.MustNewConstMetric(externalauthsDesc, prometheus.GaugeValue, float64(count), string(state))
			}
		}
	}

	// Collect active operation counts by provisioning state
	activeOperationInformer, activeOperationLister := c.backendInformers.ActiveOperations()
	if activeOperationInformer.HasSynced() {
		if operations, err := activeOperationLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list active operations for metrics")
		} else {
			counts := make(map[arm.ProvisioningState]int)
			for _, op := range operations {
				counts[op.Status]++
			}
			for state, count := range counts {
				ch <- prometheus.MustNewConstMetric(activeOperationsDesc, prometheus.GaugeValue, float64(count), string(state))
			}
		}
	}

	// Collect subscription counts by state
	subscriptionInformer, subscriptionLister := c.backendInformers.Subscriptions()
	if subscriptionInformer.HasSynced() {
		if subscriptions, err := subscriptionLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list subscriptions for metrics")
		} else {
			counts := make(map[arm.SubscriptionState]int)
			for _, sub := range subscriptions {
				counts[sub.State]++
			}
			for state, count := range counts {
				ch <- prometheus.MustNewConstMetric(subscriptionsDesc, prometheus.GaugeValue, float64(count), string(state))
			}
		}
	}

	// Collect controller status condition metrics.
	// Currently all controller documents are nested under clusters.
	// When subresource-scoped controllers are added (nodepool, externalauth),
	// this will need a per-controller resource type derivation.
	clusterControllerInformer, clusterControllerLister := c.backendInformers.ClusterControllers()
	if clusterControllerInformer.HasSynced() {
		if controllers, err := clusterControllerLister.List(ctx); err != nil {
			c.logger.Error(err, "failed to list controllers for metrics")
		} else {
			type conditionKey struct {
				controllerName, condition, status string
			}
			conditionCounts := make(map[conditionKey]int)
			for _, controller := range controllers {
				for _, cond := range controller.Status.Conditions {
					conditionCounts[conditionKey{controller.GetResourceID().Name, cond.Type, string(cond.Status)}]++
				}
			}

			for key, count := range conditionCounts {
				ch <- prometheus.MustNewConstMetric(controllerStatusConditionsDesc, prometheus.GaugeValue, float64(count), api.ClusterResourceTypeName, key.controllerName, key.condition, key.status)
			}
		}
	}
}
