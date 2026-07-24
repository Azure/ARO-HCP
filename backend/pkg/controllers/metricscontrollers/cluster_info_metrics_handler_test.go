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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestClusterInfoMetricsHandler(t *testing.T) {
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := api.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	tests := []struct {
		name            string
		spc             *api.ServiceProviderCluster
		expectedMetrics string
	}{
		{
			name: "emits cluster info with management cluster resource ID",
			spc: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				Status: api.ServiceProviderClusterStatus{
					ManagementClusterResourceID: mcResourceID,
				},
			},
			expectedMetrics: fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="%s",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(mcResourceID), resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
		},
		{
			name: "emits empty management cluster resource ID when not placed",
			spc: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				Status:         api.ServiceProviderClusterStatus{},
			},
			expectedMetrics: fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			handler := NewClusterInfoMetricsHandler(reg)
			handler.Sync(context.Background(), tt.spc)
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(tt.expectedMetrics), "backend_cluster_info"))
		})
	}
}

func TestClusterInfoMetricsHandler_DeleteCleansUp(t *testing.T) {
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/serviceProviderClusters/default"))
	mcResourceID := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)
	handler.Sync(context.Background(), &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	})
	handler.Delete(strings.ToLower(spcResourceID.String()))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_info"))
}

func TestClusterInfoMetricsHandler_UpdatesOnPlacementChange(t *testing.T) {
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/serviceProviderClusters/default"))
	clusterResourceID := spcResourceID.Parent
	mc1 := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	mc2 := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)

	handler.Sync(context.Background(), &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status:         api.ServiceProviderClusterStatus{ManagementClusterResourceID: mc1},
	})
	handler.Sync(context.Background(), &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status:         api.ServiceProviderClusterStatus{ManagementClusterResourceID: mc2},
	})

	expected := fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="%s",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(mc2), resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_info"))
}
