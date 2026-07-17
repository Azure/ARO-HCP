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
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type clusterInfoMetricsHandler struct {
	clusterInfo *prometheus.GaugeVec
}

// NewClusterInfoMetricsHandler creates a metrics handler that emits a
// backend_cluster_info gauge for each cluster, labeled with its management
// cluster placement. Use PromQL joins to combine with other per-cluster metrics.
func NewClusterInfoMetricsHandler(registerer prometheus.Registerer) Handler[*api.ServiceProviderCluster] {
	handler := &clusterInfoMetricsHandler{
		clusterInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_info",
			Help: "Info metric for clusters. Value is always 1.",
		}, []string{"resource_id", "subscription_id", "management_cluster_resource_id"}),
	}
	registerer.MustRegister(handler.clusterInfo)
	return handler
}

func (h *clusterInfoMetricsHandler) Sync(_ context.Context, serviceProviderCluster *api.ServiceProviderCluster) {
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
}

func (h *clusterInfoMetricsHandler) Delete(key string) {
	if len(key) == 0 {
		return
	}
	h.clusterInfo.DeletePartialMatch(prometheus.Labels{"resource_id": clusterResourceIDFromSPCKey(key)})
}

func clusterResourceIDFromServiceProviderCluster(serviceProviderCluster *api.ServiceProviderCluster) *azcorearm.ResourceID {
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
