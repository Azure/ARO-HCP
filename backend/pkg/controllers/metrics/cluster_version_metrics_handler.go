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
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterVersionMetricsHandler struct {
	clusterVersionInfo *prometheus.Desc
	upgradeTargetInfo  *prometheus.Desc
	upgradeTargetCount *prometheus.Desc
	monitoringEnabled  *prometheus.Desc
	readDesireLister   kubeapplierlisters.ReadDesireLister
	processID          string
	mu                 sync.RWMutex
	snapshots          map[string]clusterVersionMetricSnapshot
}

type clusterVersionMetricSnapshot struct {
	resourceID, subscriptionID, clusterUUID string
	targets                                 map[string]struct{}
	versionStates                           map[string]string
	lifecycle                               metav1.ConditionStatus
}

// NewClusterVersionMetricsHandler creates a metrics handler for cluster version metrics.
func NewClusterVersionMetricsHandler(
	prometheusRegisterer prometheus.Registerer,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) Handler[*coreapi.ServiceProviderCluster] {
	metricsHandler := &clusterVersionMetricsHandler{
		readDesireLister: readDesireLister,
		processID:        string(uuid.NewUUID()),
		snapshots:        map[string]clusterVersionMetricSnapshot{},
		clusterVersionInfo: prometheus.NewDesc(
			"backend_cluster_version_info",
			"Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, but version hasn't reflected in the cluster active versions), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics.",
			[]string{"resource_id", "subscription_id", "cluster_uuid", "version", "state"}, nil),
		upgradeTargetInfo: prometheus.NewDesc(
			"backend_cluster_upgrade_target_info",
			"Current observed and requested upgrade targets. Value is 1. Historical superseded versions are excluded.",
			[]string{"resource_id", "subscription_id", "cluster_uuid", "backend_process", "version"}, nil),
		upgradeTargetCount: prometheus.NewDesc(
			"backend_cluster_upgrade_target_count",
			"Expected current target rows in the same coherent cluster version snapshot.",
			[]string{"resource_id", "subscription_id", "cluster_uuid", "backend_process"}, nil),
		monitoringEnabled: prometheus.NewDesc(
			"backend_cluster_upgrade_monitoring_enabled",
			"Upgrade monitoring lifecycle eligibility. 1 after an active lifecycle observation, 0 for confirmed deletion or uninstallation. Absent when unknown.",
			[]string{"resource_id", "subscription_id", "cluster_uuid"}, nil),
	}
	prometheusRegisterer.MustRegister(metricsHandler)
	return metricsHandler
}

func (metricsHandler *clusterVersionMetricsHandler) Describe(ch chan<- *prometheus.Desc) {
	ch <- metricsHandler.clusterVersionInfo
	ch <- metricsHandler.upgradeTargetInfo
	ch <- metricsHandler.upgradeTargetCount
	ch <- metricsHandler.monitoringEnabled
}

func (metricsHandler *clusterVersionMetricsHandler) Collect(ch chan<- prometheus.Metric) {
	// Copy immutable snapshots once so Sync cannot split a scrape across revisions.
	metricsHandler.mu.RLock()
	snapshots := make([]clusterVersionMetricSnapshot, 0, len(metricsHandler.snapshots))
	for _, snapshot := range metricsHandler.snapshots {
		snapshots = append(snapshots, snapshot)
	}
	metricsHandler.mu.RUnlock()

	for _, snapshot := range snapshots {
		ch <- prometheus.MustNewConstMetric(metricsHandler.upgradeTargetCount, prometheus.GaugeValue, float64(len(snapshot.targets)),
			snapshot.resourceID, snapshot.subscriptionID, snapshot.clusterUUID, metricsHandler.processID)
		for version := range snapshot.targets {
			ch <- prometheus.MustNewConstMetric(metricsHandler.upgradeTargetInfo, prometheus.GaugeValue, 1,
				snapshot.resourceID, snapshot.subscriptionID, snapshot.clusterUUID, metricsHandler.processID, version)
		}
		for version, state := range snapshot.versionStates {
			ch <- prometheus.MustNewConstMetric(metricsHandler.clusterVersionInfo, prometheus.GaugeValue, 1,
				snapshot.resourceID, snapshot.subscriptionID, snapshot.clusterUUID, version, state)
		}
		switch snapshot.lifecycle {
		case metav1.ConditionTrue:
			ch <- prometheus.MustNewConstMetric(metricsHandler.monitoringEnabled, prometheus.GaugeValue, 1,
				snapshot.resourceID, snapshot.subscriptionID, snapshot.clusterUUID)
		case metav1.ConditionFalse:
			ch <- prometheus.MustNewConstMetric(metricsHandler.monitoringEnabled, prometheus.GaugeValue, 0,
				snapshot.resourceID, snapshot.subscriptionID, snapshot.clusterUUID)
		}
	}
}

func (metricsHandler *clusterVersionMetricsHandler) Sync(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) {
	snapshot := clusterVersionMetricSnapshot{
		resourceID:     resourceIDMetricLabel(serviceProviderCluster.ResourceID.Parent),
		subscriptionID: subscriptionIDMetricLabel(serviceProviderCluster.ResourceID.Parent),
		clusterUUID:    metricsHandler.clusterUUIDMetricLabel(ctx, serviceProviderCluster.ResourceID.Parent),
		targets:        map[string]struct{}{},
		versionStates:  metricsHandler.versionStatesFromServiceProviderCluster(serviceProviderCluster),
	}
	if condition := apimeta.FindStatusCondition(serviceProviderCluster.Status.Conditions, coreapi.ControlPlaneUpgradeMonitoringEnabled); condition != nil {
		snapshot.lifecycle = condition.Status
	}
	active := serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	if len(active) > 0 {
		snapshot.targets[active[0].Version.String()] = struct{}{}
	}
	if desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion; desired != nil {
		snapshot.targets[desired.String()] = struct{}{}
	}

	metricsHandler.mu.Lock()
	metricsHandler.snapshots[snapshot.resourceID] = snapshot
	metricsHandler.mu.Unlock()
}

func (metricsHandler *clusterVersionMetricsHandler) Delete(serviceProviderClusterKey string) {
	if len(serviceProviderClusterKey) == 0 {
		return
	}
	serviceProviderClusterResourceID, err := azcorearm.ParseResourceID(serviceProviderClusterKey)
	if err != nil {
		return
	}

	metricsHandler.mu.Lock()
	delete(metricsHandler.snapshots, resourceIDMetricLabel(serviceProviderClusterResourceID.Parent))
	metricsHandler.mu.Unlock()
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

func (metricsHandler *clusterVersionMetricsHandler) versionStatesFromServiceProviderCluster(serviceProviderCluster *coreapi.ServiceProviderCluster) map[string]string {
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
