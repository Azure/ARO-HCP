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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func expectedBackendClusterVersionInfoMetricHeader() string {
	return `# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, but version hasn't reflected in the cluster active versions), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics.
# TYPE backend_cluster_version_info gauge
`
}

func newTestClusterVersionMetricsHandler(t *testing.T, prometheusRegistry *prometheus.Registry, readDesireLister kubeapplierlisters.ReadDesireLister) Handler[*coreapi.ServiceProviderCluster] {
	t.Helper()
	if readDesireLister == nil {
		readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
	}
	return NewClusterVersionMetricsHandler(prometheusRegistry, readDesireLister, "test-process")
}

func newTestHostedClusterReadDesireLister(t *testing.T, clusterUUID string) kubeapplierlisters.ReadDesireLister {
	t.Helper()

	hostedCluster := &v1beta1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: v1beta1.GroupVersion.String(),
		},
		Spec: v1beta1.HostedClusterSpec{
			ClusterID: clusterUUID,
		},
	}
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)

	readDesireResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
			"sub-1", "rg", "cluster-1", kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
		),
	))

	return &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{
			{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readDesireResourceID},
				Status: kubeapplierapi.ReadDesireStatus{
					KubeContent: &kruntime.RawExtension{Raw: raw},
				},
			},
		},
	}
}

func newTestServiceProviderCluster(
	t *testing.T,
	cluster *coreapi.Cluster,
	desiredVersion string,
	activeVersions []coreapi.ServiceProviderClusterActiveVersion,
) *coreapi.ServiceProviderCluster {
	t.Helper()

	clusterResourceID := cluster.ID
	serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: serviceProviderClusterResourceID},
		Status: coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
				ActiveVersions: activeVersions,
			},
		},
	}
	if desiredVersion != "" {
		serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse(desiredVersion))
	}

	return serviceProviderCluster
}

func TestClusterVersionMetricsHandler_SetsDesiredAndActiveVersions(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.PartialUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.19"} 1
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.20"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_EmitsDesiredWhenNotYetActive(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", nil)
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="desired",subscription_id="%s",version="4.19.20"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_SkipsDuplicateActiveVersionsAfterRollback(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	// ActiveVersions is newest-first; the same z-stream can appear twice after a rollback.
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.PartialUpdate},
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.19"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_CapsReportedActiveVersions(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	// ActiveVersions is newest-first; only the three newest history entries are reported.
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.PartialUpdate},
		{Version: ptr.To(semver.MustParse("4.19.21")), State: configv1.PartialUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.PartialUpdate},
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	clusterUUID := "11111111-1111-1111-1111-111111111111"

	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.22"} 1
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.21"} 1
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.20"} 1
`, clusterUUID, resourceID, subscriptionID, clusterUUID, resourceID, subscriptionID, clusterUUID, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_ReplacesDesiredWhenVersionBecomesActive(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_VersionStateTransitionDeletesOldSeries(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.PartialUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	}
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.19"} 1
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_DeleteCleansUpMetrics(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", nil)
	serviceProviderCluster.Status.Conditions = []metav1.Condition{{
		Type: coreapi.ControlPlaneUpgradeMonitoringEnabled, Status: metav1.ConditionTrue,
	}}
	handler.Sync(ctx, serviceProviderCluster)

	spcKey, err := resourceIDStoreKey(serviceProviderCluster)
	require.NoError(t, err)
	handler.Delete(spcKey)

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""),
		"backend_cluster_version_info", "backend_cluster_upgrade_target_info", "backend_cluster_upgrade_target_count", "backend_cluster_upgrade_converged", "backend_cluster_upgrade_monitoring_enabled"))
}

func TestClusterVersionMetricsHandler_UpgradeTargetsAndLifecycle(t *testing.T) {
	tests := []struct {
		name        string
		desired     string
		active      []coreapi.ServiceProviderClusterActiveVersion
		lifecycle   metav1.ConditionStatus
		wantTargets []string
		wantEnabled string
		converged   bool
	}{
		{
			name: "missing newest version cannot panic or report convergence", desired: "4.20.43",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: nil, State: configv1.CompletedUpdate},
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.CompletedUpdate},
			},
			wantTargets: []string{"4.20.43"}, wantEnabled: "1",
		},
		{
			name: "matching current and desired completed", desired: "4.20.43",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.CompletedUpdate},
			},
			wantTargets: []string{"4.20.43"}, converged: true,
		},
		{
			name: "historical completed desired is not current", desired: "4.20.42",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.CompletedUpdate},
				{Version: ptr.To(semver.MustParse("4.20.42")), State: configv1.CompletedUpdate},
			},
			wantTargets: []string{"4.20.42", "4.20.43"},
		},
		{
			name: "historical completion does not override newest partial", desired: "4.20.43",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.PartialUpdate},
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.CompletedUpdate},
			},
			wantTargets: []string{"4.20.43"},
		},
		{
			name:    "superseded partial is diagnostic only",
			desired: "4.20.43",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.PartialUpdate},
				{Version: ptr.To(semver.MustParse("4.20.42")), State: configv1.PartialUpdate},
				{Version: ptr.To(semver.MustParse("4.20.41")), State: configv1.CompletedUpdate},
			},
			lifecycle: metav1.ConditionTrue, wantTargets: []string{"4.20.43"}, wantEnabled: "1",
		},
		{
			name:    "new desired does not erase the running upgrade",
			desired: "4.20.44",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.PartialUpdate},
			},
			lifecycle: metav1.ConditionTrue, wantTargets: []string{"4.20.43", "4.20.44"}, wantEnabled: "1",
		},
		{
			name:    "rollback target need not be numerically highest",
			desired: "4.20.41",
			active: []coreapi.ServiceProviderClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.20.41")), State: configv1.PartialUpdate},
				{Version: ptr.To(semver.MustParse("4.20.43")), State: configv1.PartialUpdate},
			},
			lifecycle: metav1.ConditionTrue, wantTargets: []string{"4.20.41"}, wantEnabled: "1",
		},
		{
			name:    "confirmed deletion explicitly disables monitoring",
			desired: "4.20.43", lifecycle: metav1.ConditionFalse,
			wantTargets: []string{"4.20.43"}, wantEnabled: "0",
		},
		{
			name:    "unknown lifecycle is not zero",
			desired: "4.20.43", lifecycle: metav1.ConditionUnknown,
			wantTargets: []string{"4.20.43"},
		},
		{
			name:    "unobserved lifecycle is not zero",
			desired: "4.20.43", wantTargets: []string{"4.20.43"}, wantEnabled: "1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			handler := newTestClusterVersionMetricsHandler(t, registry, nil)
			metricsHandler, ok := handler.(*clusterVersionMetricsHandler)
			require.True(t, ok)
			cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
			spc := newTestServiceProviderCluster(t, cluster, tt.desired, tt.active)
			if tt.lifecycle != "" {
				spc.Status.Conditions = []metav1.Condition{{
					Type: coreapi.ControlPlaneUpgradeMonitoringEnabled, Status: tt.lifecycle,
				}}
			}
			// A second sync must replace target and lifecycle series, not accumulate them.
			handler.Sync(context.Background(), newTestServiceProviderCluster(t, cluster, "4.20.40", nil))
			handler.Sync(context.Background(), spc)
			var expected strings.Builder
			if tt.lifecycle == "" {
				tt.wantEnabled = "1"
			}
			expected.WriteString("# HELP backend_cluster_upgrade_target_info Current observed and requested upgrade targets. Value is 1. Historical superseded versions are excluded.\n# TYPE backend_cluster_upgrade_target_info gauge\n")
			for _, target := range tt.wantTargets {
				fmt.Fprintf(&expected, "backend_cluster_upgrade_target_info{backend_process=\"%s\",cluster_uuid=\"\",resource_id=\"%s\",subscription_id=\"%s\",version=\"%s\"} 1\n",
					metricsHandler.processID, resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID), target)
			}
			expected.WriteString("# HELP backend_cluster_upgrade_target_count Expected current target rows in the same coherent cluster version snapshot.\n# TYPE backend_cluster_upgrade_target_count gauge\n")
			fmt.Fprintf(&expected, "backend_cluster_upgrade_target_count{backend_process=\"%s\",cluster_uuid=\"\",resource_id=\"%s\",subscription_id=\"%s\"} %d\n",
				metricsHandler.processID, resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID), len(tt.wantTargets))
			converged := 0
			if tt.converged {
				converged = 1
			}
			expected.WriteString("# HELP backend_cluster_upgrade_converged Whether resolved desired equals the newest observed current version and that entry is completed.\n# TYPE backend_cluster_upgrade_converged gauge\n")
			fmt.Fprintf(&expected, "backend_cluster_upgrade_converged{backend_process=\"%s\",cluster_uuid=\"\",resource_id=\"%s\",subscription_id=\"%s\"} %d\n",
				metricsHandler.processID, resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID), converged)
			if tt.wantEnabled != "" {
				expected.WriteString("# HELP backend_cluster_upgrade_monitoring_enabled Upgrade monitoring lifecycle eligibility. 1 unless teardown was observed, 0 for confirmed deletion or uninstallation. An absent condition implies eligibility, not a fresh CS observation.\n# TYPE backend_cluster_upgrade_monitoring_enabled gauge\n")
				fmt.Fprintf(&expected, "backend_cluster_upgrade_monitoring_enabled{backend_process=\"%s\",cluster_uuid=\"\",resource_id=\"%s\",subscription_id=\"%s\"} %s\n",
					metricsHandler.processID, resourceIDMetricLabel(cluster.ID), subscriptionIDMetricLabel(cluster.ID), tt.wantEnabled)
			}
			require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(expected.String()),
				"backend_cluster_upgrade_target_info", "backend_cluster_upgrade_target_count", "backend_cluster_upgrade_converged", "backend_cluster_upgrade_monitoring_enabled"))
		})
	}
}

func TestClusterVersionMetricsController_SyncResource(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(serviceProviderCluster))

	controller := &Controller[*coreapi.ServiceProviderCluster]{
		name:    "ClusterVersionMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(serviceProviderCluster)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(ctx, key))

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := expectedBackendClusterVersionInfoMetricHeader() + fmt.Sprintf(`backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, "11111111-1111-1111-1111-111111111111", resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_CollectUsesOneSnapshot(t *testing.T) {
	registry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, registry, nil)
	collector, ok := handler.(prometheus.Collector)
	require.True(t, ok)
	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	active := []coreapi.ServiceProviderClusterActiveVersion{{
		Version: ptr.To(semver.MustParse("4.20.42")), State: configv1.CompletedUpdate,
	}}
	handler.Sync(context.Background(), newTestServiceProviderCluster(t, cluster, "4.20.42", active))

	metrics := make(chan prometheus.Metric)
	go func() {
		collector.Collect(metrics)
		close(metrics)
	}()
	first := <-metrics
	// Change the desired target while the scrape is paused after its count.
	handler.Sync(context.Background(), newTestServiceProviderCluster(t, cluster, "4.20.43", active))
	observed := []prometheus.Metric{first}
	for metric := range metrics {
		observed = append(observed, metric)
	}
	require.Len(t, observed, 5)
	frozen := prometheus.NewRegistry()
	frozen.MustRegister(prometheus.CollectorFunc(func(ch chan<- prometheus.Metric) {
		for _, metric := range observed {
			ch <- metric
		}
	}))
	oldFamilies, err := frozen.Gather()
	require.NoError(t, err)
	for _, family := range oldFamilies {
		if family.GetName() == "backend_cluster_upgrade_target_count" {
			require.Equal(t, float64(1), family.Metric[0].GetGauge().GetValue())
		}
		if family.GetName() == "backend_cluster_upgrade_converged" {
			require.Equal(t, float64(1), family.Metric[0].GetGauge().GetValue())
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "version" {
					require.Equal(t, "4.20.42", label.GetValue(), "a scrape must not mix target revisions")
				}
			}
		}
	}
	require.Contains(t, first.Desc().String(), "backend_cluster_upgrade_target_count")

	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		switch family.GetName() {
		case "backend_cluster_upgrade_target_count":
			require.Equal(t, float64(2), family.Metric[0].GetGauge().GetValue())
		case "backend_cluster_upgrade_target_info":
			require.Len(t, family.Metric, 2)
		case "backend_cluster_upgrade_converged":
			require.Equal(t, float64(0), family.Metric[0].GetGauge().GetValue())
		}
	}
}

func TestClusterVersionMetricsController_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, "11111111-1111-1111-1111-111111111111"))

	cluster := newTestCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(serviceProviderCluster))

	controller := &Controller[*coreapi.ServiceProviderCluster]{
		name:    "ClusterVersionMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(serviceProviderCluster)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(ctx, key))
	require.NoError(t, indexer.Delete(serviceProviderCluster))
	require.NoError(t, controller.syncResource(ctx, key))

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""), "backend_cluster_version_info"))
}
