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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func seedTestServiceProviderCluster(
	t *testing.T,
	ctx context.Context,
	mockResourcesDBClient *databasetesting.MockResourcesDBClient,
	clusterName string,
	desiredVersion string,
	activeVersions []api.HCPClusterActiveVersion,
) *api.HCPOpenShiftCluster {
	t.Helper()

	cluster := newTestCluster(t, clusterName, arm.ProvisioningStateSucceeded, nil)
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

	_, err := mockResourcesDBClient.ServiceProviderClusters(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	).Create(ctx, serviceProviderCluster, nil)
	require.NoError(t, err)

	return cluster
}

func TestClusterVersionMetricsHandler_SetsDesiredAndActiveVersions(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := seedTestServiceProviderCluster(t, ctx, mockResourcesDBClient, "cluster-1", "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.19")), State: configv1.CompletedUpdate},
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.PartialUpdate},
	})
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

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
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := seedTestServiceProviderCluster(t, ctx, mockResourcesDBClient, "cluster-1", "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

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
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := seedTestServiceProviderCluster(t, ctx, mockResourcesDBClient, "cluster-1", "4.19.20", nil)
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})
	versionMetricsHandler.Delete(resourceIDMetricLabel(cluster.ID))

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_NoMetricsWhenServiceProviderClusterMissing(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := newTestCluster(t, "cluster-1", arm.ProvisioningStateSucceeded, nil)
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""), "backend_cluster_version_info"))
}

func TestClusterVersionMetricsHandler_ClearsMetricsWhenServiceProviderClusterDeleted(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := seedTestServiceProviderCluster(t, ctx, mockResourcesDBClient, "cluster-1", "4.19.20", nil)
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

	err := mockResourcesDBClient.ServiceProviderClusters(
		cluster.ID.SubscriptionID,
		cluster.ID.ResourceGroupName,
		cluster.ID.Name,
	).Delete(ctx, api.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(""), "backend_cluster_version_info"))
}

// Stubs below simulate a transient ServiceProviderCluster Get failure for
// TestClusterVersionMetricsHandler_RetainsMetricsOnTransientGetError.
type getErrorServiceProviderClusterCRUD struct {
	database.ServiceProviderClusterCRUD
	getErr error
}

func (c *getErrorServiceProviderClusterCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderCluster, error) {
	return nil, c.getErr
}

type serviceProviderClusterGetErrorDBClient struct {
	*databasetesting.MockResourcesDBClient
	getErr error
}

func (c *serviceProviderClusterGetErrorDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	return &getErrorServiceProviderClusterCRUD{
		ServiceProviderClusterCRUD: c.MockResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName),
		getErr:                     c.getErr,
	}
}

func TestClusterVersionMetricsHandler_RetainsMetricsOnTransientGetError(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	prometheusRegistry := prometheus.NewRegistry()
	versionMetricsHandler := newClusterVersionMetricsHandler(prometheusRegistry, mockResourcesDBClient)

	cluster := seedTestServiceProviderCluster(t, ctx, mockResourcesDBClient, "cluster-1", "4.19.20", []api.HCPClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.19.20")), State: configv1.CompletedUpdate},
	})
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

	resourceID := resourceIDMetricLabel(cluster.ID)
	subscriptionID := subscriptionIDMetricLabel(cluster.ID)
	expected := fmt.Sprintf(`# HELP backend_cluster_version_info Information about cluster versions. Value is 1 when version is in the specified state. States: desired (target selected, upgrade not started), partial (upgrade in progress), completed (upgrade finished). Use partial and completed for fleet and upgrade-progress metrics; desired is pre-upgrade only.
# TYPE backend_cluster_version_info gauge
backend_cluster_version_info{resource_id="%s",state="completed",subscription_id="%s",version="4.19.20"} 1
`, resourceID, subscriptionID)

	versionMetricsHandler.resourcesDBClient = &serviceProviderClusterGetErrorDBClient{
		MockResourcesDBClient: mockResourcesDBClient,
		getErr:                errors.New("transient database error"),
	}
	versionMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})

	require.NoError(t, testutil.GatherAndCompare(prometheusRegistry, strings.NewReader(expected), "backend_cluster_version_info"))
}
