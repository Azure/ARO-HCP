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
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestClusterInfoMetricsHandler(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	mcResourceIDOther := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"))

	tests := []struct {
		name            string
		spc             *coreapi.ServiceProviderCluster
		expectedMetrics string
	}{
		{
			name: "observed placement reported from status",
			spc: &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
				Spec: coreapi.ServiceProviderClusterSpec{
					ManagementClusterResourceID: mcResourceID,
				},
				Status: coreapi.ServiceProviderClusterStatus{
					ManagementClusterResourceID: mcResourceID,
				},
			},
			expectedMetrics: fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="%s",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(mcResourceID), resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
		},
		{
			name: "no observed placement, empty management cluster resource ID",
			spc: &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
				Status:         coreapi.ServiceProviderClusterStatus{},
			},
			expectedMetrics: fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
		},
		{
			name: "management cluster resource id mirrors the observed status placement",
			spc: &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
				Spec: coreapi.ServiceProviderClusterSpec{
					ManagementClusterResourceID: mcResourceID,
				},
				Status: coreapi.ServiceProviderClusterStatus{
					ManagementClusterResourceID: mcResourceIDOther,
				},
			},
			expectedMetrics: fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="%s",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(mcResourceIDOther), resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
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
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	})
	handler.Delete(strings.ToLower(spcResourceID.String()))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_info"))
}

func TestClusterInfoMetricsHandler_UpdatesOnManagementClusterChange(t *testing.T) {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/serviceProviderClusters/default"))
	clusterResourceID := spcResourceID.Parent
	mc1 := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	mc2 := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)

	// First observed on mc1.
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Spec:           coreapi.ServiceProviderClusterSpec{ManagementClusterResourceID: mc1},
		Status:         coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mc1},
	})
	// Observed placement moves to mc2.
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Spec:           coreapi.ServiceProviderClusterSpec{ManagementClusterResourceID: mc1},
		Status:         coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mc2},
	})

	// Only the current series survives: DeletePartialMatch on resource_id clears
	// the stale management_cluster_resource_id=mc1 series so no duplicate lingers
	// after the label value changes.
	expected := fmt.Sprintf(`# HELP backend_cluster_info Info metric for clusters. Value is always 1.
# TYPE backend_cluster_info gauge
backend_cluster_info{management_cluster_resource_id="%s",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(mc2), resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_info"))
}

func TestClusterInfoMetricsHandler_PlacementTime(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	// A real placement timestamp; the gauge value is its unix-seconds representation.
	placedAt := metav1.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC)
	// Format the expected value exactly as the prometheus text exposition does
	// (strconv 'g', -1, 64) so large unix timestamps compare correctly.
	placedValue := strconv.FormatFloat(float64(placedAt.Unix()), 'g', -1, 64)

	placementTimeHeader := `# HELP backend_cluster_placement_time_seconds Unix timestamp (seconds) at which the scheduler recorded a management-cluster placement (Spec.ManagementClusterPlacementTime). Emitted per cluster once placement intent is set; kube-state-metrics style — compute time-to-placement in PromQL against the cluster's creation timestamp.
# TYPE backend_cluster_placement_time_seconds gauge
`

	tests := []struct {
		name     string
		spec     coreapi.ServiceProviderClusterSpec
		expected string
	}{
		{
			name: "emitted as the placement unix timestamp when intent and timestamp are set",
			spec: coreapi.ServiceProviderClusterSpec{
				ManagementClusterResourceID:    mcResourceID,
				ManagementClusterPlacementTime: &placedAt,
			},
			expected: placementTimeHeader + fmt.Sprintf(`backend_cluster_placement_time_seconds{resource_id="%s",subscription_id="%s"} %s
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID), placedValue),
		},
		{
			name:     "not emitted while placement intent is unset",
			spec:     coreapi.ServiceProviderClusterSpec{ManagementClusterPlacementTime: &placedAt},
			expected: "",
		},
		{
			name:     "not emitted when the placement timestamp is nil",
			spec:     coreapi.ServiceProviderClusterSpec{ManagementClusterResourceID: mcResourceID},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			handler := NewClusterInfoMetricsHandler(reg)
			handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
				Spec:           tc.spec,
			})
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(tc.expected), "backend_cluster_placement_time_seconds"))
		})
	}
}

func TestClusterInfoMetricsHandler_PlacementTimeClearedWhenIntentRemoved(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	placedAt := metav1.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC)

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)

	// Placed with a timestamp: the series is emitted.
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Spec: coreapi.ServiceProviderClusterSpec{
			ManagementClusterResourceID:    mcResourceID,
			ManagementClusterPlacementTime: &placedAt,
		},
	})
	// Intent cleared: the placement-time series must be removed.
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
	})
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_placement_time_seconds"))
}

func TestClusterInfoMetricsHandler_PlacementTimeDeletedOnDelete(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	placedAt := metav1.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC)

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Spec: coreapi.ServiceProviderClusterSpec{
			ManagementClusterResourceID:    mcResourceID,
			ManagementClusterPlacementTime: &placedAt,
		},
	})
	handler.Delete(strings.ToLower(spcResourceID.String()))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_placement_time_seconds"))
}
