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

// Cluster scheduling phases emitted as the phase label of backend_cluster_phase_info.
const (
	// clusterPhaseInitializing is emitted before the scheduler has resolved
	// placement (Spec.ManagementClusterResourceID is nil).
	clusterPhaseInitializing = "Initializing"
	// clusterPhaseScheduled is emitted once the scheduler has recorded placement
	// intent (Spec.ManagementClusterResourceID is set).
	clusterPhaseScheduled = "Scheduled"
)

type clusterInfoMetricsHandler struct {
	clusterInfo *prometheus.GaugeVec
	phaseInfo   *prometheus.GaugeVec
}

// NewClusterInfoMetricsHandler creates a metrics handler that emits, per cluster:
//
//   - backend_cluster_info: an info gauge (value always 1) carrying the cluster's
//     resource ID, subscription ID, and observed management-cluster placement
//     (management_cluster_resource_id, mirrored from
//     ServiceProviderCluster.Status.ManagementClusterResourceID). Use PromQL joins
//     to combine it with other per-cluster metrics.
//
//   - backend_cluster_phase_info: a kube-state-metrics-style info gauge (value
//     always 1) carrying the cluster's current scheduling phase as a label.
//     Exactly one series per cluster: phase="Scheduled" once the scheduler has
//     recorded placement intent (ServiceProviderCluster.Spec.
//     ManagementClusterResourceID is set), otherwise phase="Initializing".
func NewClusterInfoMetricsHandler(registerer prometheus.Registerer) Handler[*coreapi.ServiceProviderCluster] {
	handler := &clusterInfoMetricsHandler{
		clusterInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_info",
			Help: "Info metric for clusters. Value is always 1.",
		}, []string{"resource_id", "subscription_id", "management_cluster_resource_id"}),
		phaseInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_phase_info",
			Help: "Current internal lifecycle phase of the cluster. Value is always 1. Phase: Initializing, Scheduled",
		}, []string{"resource_id", "subscription_id", "phase"}),
	}
	registerer.MustRegister(handler.clusterInfo, handler.phaseInfo)
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

	// Phase (kube-state-metrics style): expose exactly one series per cluster
	// carrying its current scheduling phase as a label. Clear any prior series
	// first so a phase transition does not leave a stale series behind.
	h.phaseInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
	h.phaseInfo.With(prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
		"phase":           clusterPhase(serviceProviderCluster),
	}).Set(1.0)
}

// clusterPhase returns the cluster's current scheduling phase for the
// backend_cluster_phase_info metric: clusterPhaseScheduled once the scheduler has
// recorded placement intent (Spec.ManagementClusterResourceID is set), otherwise
// clusterPhaseInitializing.
//
// TODO: enhance with more relevant cluster lifecycle phases.
func clusterPhase(serviceProviderCluster *coreapi.ServiceProviderCluster) string {
	if serviceProviderCluster.Spec.ManagementClusterResourceID != nil {
		return clusterPhaseScheduled
	}
	return clusterPhaseInitializing
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
	h.phaseInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
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
