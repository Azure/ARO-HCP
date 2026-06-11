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

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
)

const testClusterUUID = "11111111-1111-1111-1111-111111111111"

func newTestClusterVersionMetricsHandler(t *testing.T, prometheusRegistry *prometheus.Registry, readDesireLister dblisters.ReadDesireLister) Handler[*api.ServiceProviderCluster] {
	t.Helper()
	if readDesireLister == nil {
		readDesireLister = &internallistertesting.SliceReadDesireLister{}
	}
	return NewClusterVersionMetricsHandler(prometheusRegistry, readDesireLister)
}

func newTestHostedClusterReadDesireLister(t *testing.T, clusterUUID string) dblisters.ReadDesireLister {
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

	readDesireResourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(
			"sub-1", "rg", "cluster-1", maestrohelpers.ReadDesireNameReadonlyHostedCluster,
		),
	))

	return &internallistertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{
			{
				CosmosMetadata: api.CosmosMetadata{ResourceID: readDesireResourceID},
				Status: kubeapplier.ReadDesireStatus{
					KubeContent: &kruntime.RawExtension{Raw: raw},
				},
			},
		},
	}
}

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
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, testClusterUUID))

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
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.19"} 1
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="partial",subscription_id="%s",version="4.19.20"} 1
`, testClusterUUID, resourceID, subscriptionID, testClusterUUID, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_ReplacesDesiredWhenVersionBecomesActive(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, testClusterUUID))

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	serviceProviderCluster := newTestServiceProviderCluster(t, cluster, "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})
	handler.Sync(ctx, serviceProviderCluster)

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)

	expected := fmt.Sprintf(`# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.
# TYPE backend_cluster_version_info gauge
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, testClusterUUID, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_DeleteCleansUpMetrics(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, testClusterUUID))

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
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, testClusterUUID))

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
backend_cluster_version_info{cluster_uuid="%s",resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, testClusterUUID, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsController_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	ctx := context.Background()
	prometheusRegistry := prometheus.NewRegistry()
	handler := newTestClusterVersionMetricsHandler(t, prometheusRegistry, newTestHostedClusterReadDesireLister(t, testClusterUUID))

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
