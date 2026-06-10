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

	"github.com/prometheus/client_golang/prometheus"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterVersionMetricsHandler struct {
	clusterVersionInfo *prometheus.GaugeVec
	resourcesDBClient  database.ResourcesDBClient
}

func newClusterVersionMetricsHandler(
	prometheusRegisterer prometheus.Registerer,
	resourcesDBClient database.ResourcesDBClient,
) *clusterVersionMetricsHandler {
	metricsHandler := &clusterVersionMetricsHandler{
		clusterVersionInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_version_info",
			Help: "Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.",
		}, []string{"resource_id", "subscription_id", "version", "state"}),
		resourcesDBClient: resourcesDBClient,
	}
	prometheusRegisterer.MustRegister(metricsHandler.clusterVersionInfo)
	return metricsHandler
}

func (metricsHandler *clusterVersionMetricsHandler) Sync(ctx context.Context, clusterVersionObject clusterMetricsObject) {
	clusterResourceID := clusterVersionObject.ResourceID()
	resourceID := resourceIDMetricLabel(clusterResourceID)
	if len(resourceID) == 0 {
		return
	}
	subscriptionID := subscriptionIDMetricLabel(clusterResourceID)

	serviceProviderCluster, err := metricsHandler.resourcesDBClient.ServiceProviderClusters(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	).Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil {
		if database.IsNotFoundError(err) {
			metricsHandler.clusterVersionInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
		} else {
			utils.LoggerFromContext(ctx).Error(err, "failed to get ServiceProviderCluster for cluster version metrics; retaining previous metrics",
				"resource_id", resourceID)
		}
		return
	}

	metricsHandler.clusterVersionInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})

	for version, state := range versionStatesFromServiceProviderCluster(serviceProviderCluster) {
		metricsHandler.clusterVersionInfo.With(prometheus.Labels{
			"resource_id":     resourceID,
			"subscription_id": subscriptionID,
			"version":         version,
			"state":           state,
		}).Set(1.0)
	}
}

func (metricsHandler *clusterVersionMetricsHandler) Delete(resourceIDKey string) {
	if len(resourceIDKey) == 0 {
		return
	}

	metricsHandler.clusterVersionInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceIDKey})
}

func versionStatesFromServiceProviderCluster(serviceProviderCluster *api.ServiceProviderCluster) map[string]string {
	versionStates := make(map[string]string)
	if serviceProviderCluster == nil {
		return versionStates
	}

	// Desired is emitted only when the target z-stream is not yet active. Active versions
	// (partial or completed) override desired for the same version string.
	if serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion != nil {
		versionStates[serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion.String()] = "desired"
	}

	for _, activeVersion := range serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions {
		if activeVersion.Version == nil {
			continue
		}

		state := "completed"
		if activeVersion.State == configv1.PartialUpdate {
			state = "partial"
		}

		versionStates[activeVersion.Version.String()] = state
	}

	return versionStates
}
