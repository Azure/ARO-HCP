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
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type clusterInfoMetricsHandler struct {
	clusterInfo   *prometheus.GaugeVec
	placementTime *prometheus.GaugeVec
}

// NewClusterInfoMetricsHandler creates a metrics handler that emits, per cluster:
//
//   - backend_cluster_info: an info gauge (value always 1) carrying the cluster's
//     resource ID, subscription ID, and observed management-cluster placement
//     (management_cluster_resource_id, mirrored from
//     ServiceProviderCluster.Status.ManagementClusterResourceID). Use PromQL joins
//     to combine it with other per-cluster metrics.
//
//   - backend_cluster_placement_time_seconds: a kube-state-metrics-style gauge
//     emitted only once the scheduler has recorded placement intent
//     (ServiceProviderCluster.Spec.ManagementClusterResourceID is set). Its value
//     is the unix timestamp (seconds) at which placement was recorded
//     (Spec.ManagementClusterPlacementTime) — a stable timestamp, not a duration.
//     Compute time-to-placement in PromQL against the cluster's creation timestamp.
func NewClusterInfoMetricsHandler(registerer prometheus.Registerer) Handler[*coreapi.ServiceProviderCluster] {
	handler := &clusterInfoMetricsHandler{
		clusterInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_info",
			Help: "Info metric for clusters. Value is always 1.",
		}, []string{"resource_id", "subscription_id", "management_cluster_resource_id"}),
		placementTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_placement_time_seconds",
			Help: "Unix timestamp (seconds) at which the scheduler recorded a management-cluster placement (Spec.ManagementClusterPlacementTime). Emitted per cluster once placement intent is set; kube-state-metrics style — compute time-to-placement in PromQL against the cluster's creation timestamp.",
		}, []string{"resource_id", "subscription_id"}),
	}
	registerer.MustRegister(handler.clusterInfo, handler.placementTime)
	return handler
}

func (h *clusterInfoMetricsHandler) Sync(_ context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) {
	clusterResourceID := clusterResourceIDFromServiceProviderCluster(serviceProviderCluster)
	if clusterResourceID == nil {
		return
	}
	resourceID := resourceIDMetricLabel(clusterResourceID)
	subscriptionID := subscriptionIDMetricLabel(clusterResourceID)
	managementClusterResourceID := resourceIDMetricLabel(serviceProviderCluster.Status.ManagementClusterResourceID)

	h.clusterInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
	h.clusterInfo.With(prometheus.Labels{
		"resource_id":                    resourceID,
		"subscription_id":                subscriptionID,
		"management_cluster_resource_id": managementClusterResourceID,
	}).Set(1.0)

	// Placement time (kube-state-metrics style): expose the timestamp at which the
	// scheduler recorded placement intent (Spec.ManagementClusterPlacementTime) as
	// unix seconds. Clear any prior series first so an unplaced cluster — or one
	// without a recorded placement timestamp — carries no stale series.
	h.placementTime.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
	if serviceProviderCluster.Spec.ManagementClusterResourceID == nil || serviceProviderCluster.Spec.ManagementClusterPlacementTime == nil {
		return
	}
	h.placementTime.With(prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
	}).Set(float64(serviceProviderCluster.Spec.ManagementClusterPlacementTime.Unix()))
}

func (h *clusterInfoMetricsHandler) Delete(key string) {
	if len(key) == 0 {
		return
	}
	resourceID := clusterResourceIDFromSPCKey(key)
	if len(resourceID) == 0 {
		return
	}
	h.clusterInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
	h.placementTime.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
}

func clusterResourceIDFromServiceProviderCluster(serviceProviderCluster *coreapi.ServiceProviderCluster) *azcorearm.ResourceID {
	if serviceProviderCluster == nil || serviceProviderCluster.ResourceID == nil {
		return nil
	}
	return serviceProviderCluster.ResourceID.Parent
}

func clusterResourceIDFromSPCKey(spcKey string) string {
	resourceID, err := azcorearm.ParseResourceID(spcKey)
	if err != nil || resourceID == nil {
		return ""
	}
	if resourceID.Parent == nil {
		return ""
	}
	return strings.ToLower(resourceID.Parent.String())
}
