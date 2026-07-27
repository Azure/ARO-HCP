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

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterVersionMetricsHandler struct {
	clusterVersionInfo *prometheus.GaugeVec
	readDesireLister   dblisters.ReadDesireLister
}

// NewClusterVersionMetricsHandler creates a metrics handler for cluster version metrics.
func NewClusterVersionMetricsHandler(
	prometheusRegisterer prometheus.Registerer,
	readDesireLister dblisters.ReadDesireLister,
) Handler[*api.ServiceProviderCluster] {
	metricsHandler := &clusterVersionMetricsHandler{
		readDesireLister: readDesireLister,
		clusterVersionInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_version_info",
			Help: "Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, but version hasn't reflected in the cluster active versions), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics.",
		}, []string{"resource_id", "subscription_id", "cluster_uuid", "version", "state"}),
	}
	prometheusRegisterer.MustRegister(metricsHandler.clusterVersionInfo)
	return metricsHandler
}

func (metricsHandler *clusterVersionMetricsHandler) Sync(ctx context.Context, serviceProviderCluster *api.ServiceProviderCluster) {
	resourceID := resourceIDMetricLabel(serviceProviderCluster.ResourceID.Parent)
	subscriptionID := subscriptionIDMetricLabel(serviceProviderCluster.ResourceID.Parent)
	clusterUUID := metricsHandler.clusterUUIDMetricLabel(ctx, serviceProviderCluster.ResourceID.Parent)

	metricsHandler.clusterVersionInfo.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})

	for version, state := range metricsHandler.versionStatesFromServiceProviderCluster(serviceProviderCluster) {
		metricsHandler.clusterVersionInfo.With(prometheus.Labels{
			"resource_id":     resourceID,
			"subscription_id": subscriptionID,
			"cluster_uuid":    clusterUUID,
			"version":         version,
			"state":           state,
		}).Set(1.0)
	}
}

func (metricsHandler *clusterVersionMetricsHandler) Delete(serviceProviderClusterKey string) {
	if len(serviceProviderClusterKey) == 0 {
		return
	}
	serviceProviderClusterResourceID, err := azcorearm.ParseResourceID(serviceProviderClusterKey)
	if err != nil {
		return
	}

	metricsHandler.clusterVersionInfo.DeletePartialMatch(prometheus.Labels{
		"resource_id": resourceIDMetricLabel(serviceProviderClusterResourceID.Parent),
	})
}

func (metricsHandler *clusterVersionMetricsHandler) clusterUUIDMetricLabel(
	ctx context.Context,
	clusterResourceID *azcorearm.ResourceID,
) string {
	clusterUUID, found, err := kubeapplierhelpers.GetCachedHostedClusterUUIDForCluster(
		ctx,
		metricsHandler.readDesireLister,
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)
	if err != nil {
		logger := utils.LoggerFromContext(ctx)
		logger.Error(utils.TrackError(err), "error getting cluster UUID, continuing with empty")
	} else if !found {
		logger := utils.LoggerFromContext(ctx)
		logger.V(1).Info("missing cluster UUID, continuing with empty")
	}
	if err != nil || !found {
		return ""
	}
	return strings.ToLower(clusterUUID.String())
}

func (metricsHandler *clusterVersionMetricsHandler) versionStatesFromServiceProviderCluster(serviceProviderCluster *api.ServiceProviderCluster) map[string]string {
	versionStates := make(map[string]string)

	activeVersions := serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	// Bound concurrent version time series per cluster: the version we are on or moving to and the two previous versions.
	const maxReportedActiveVersions = 3
	if len(activeVersions) > maxReportedActiveVersions {
		activeVersions = activeVersions[:maxReportedActiveVersions]
	}
	for _, activeVersion := range activeVersions {
		version := activeVersion.Version.String()
		// ActiveVersions is newest-first; skip duplicates that can appear after a rollback to the same z-stream.
		if _, ok := versionStates[version]; ok {
			continue
		}
		versionStates[version] = strings.ToLower(string(activeVersion.State))
	}

	// Desired is emitted only when the target z-stream is not yet active.
	if serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion != nil {
		desiredVersion := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion.String()
		if _, ok := versionStates[desiredVersion]; !ok {
			versionStates[desiredVersion] = "desired"
		}
	}

	return versionStates
}
