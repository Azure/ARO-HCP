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
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func newTestServiceProviderCluster(
	t *testing.T,
	cluster *api.HCPOpenShiftCluster,
	desiredVersion string,
	activeVersions []api.HCPClusterActiveVersion,
) *api.ServiceProviderCluster {
	t.Helper()

	clusterResourceID := cluster.ID
	serviceProviderClusterResourceID := api.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: serviceProviderClusterResourceID},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
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
	handler := NewClusterVersionMetricsHandler(prometheusRegistry)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.PartialUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := fmt.Sprintf(`# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.
# TYPE backend_cluster_version_info gauge
backend_cluster_version_info{resource_id="%s",state="completed",subscription_id="%s",version="4.19.19"} 1
backend_cluster_version_info{resource_id="%s",state="partial",subscription_id="%s",version="4.19.20"} 1
`, resourceID, subscriptionID, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_ReplacesDesiredWhenVersionBecomesActive(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := NewClusterVersionMetricsHandler(prometheusRegistry)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := fmt.Sprintf(`# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.
# TYPE backend_cluster_version_info gauge
backend_cluster_version_info{resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_DeleteCleansUpMetrics(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := NewClusterVersionMetricsHandler(prometheusRegistry)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", nil)
	handler.Sync(ctx, serviceProviderCluster)

	spcKey, err := resourceIDStoreKey(serviceProviderCluster)
	require.NoError(t, err)
	handler.Delete(spcKey)

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsController_SyncResource(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := NewClusterVersionMetricsHandler(prometheusRegistry)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(serviceProviderCluster))

	controller := &Controller[*api.ServiceProviderCluster]{
		name:    "ClusterVersionMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(serviceProviderCluster)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(ctx, key))

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := fmt.Sprintf(`# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.
# TYPE backend_cluster_version_info gauge
backend_cluster_version_info{resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsController_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := NewClusterVersionMetricsHandler(prometheusRegistry)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(serviceProviderCluster))

	controller := &Controller[*api.ServiceProviderCluster]{
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
