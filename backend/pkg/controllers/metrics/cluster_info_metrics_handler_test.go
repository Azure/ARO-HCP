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
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

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

func TestClusterInfoMetricsHandler_PhaseInfo(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	phaseHeader := `# HELP backend_cluster_phase_info Current internal lifecycle phase of the cluster. Value is always 1. Phase: Initializing, Scheduled
# TYPE backend_cluster_phase_info gauge
`

	tests := []struct {
		name     string
		spec     coreapi.ServiceProviderClusterSpec
		expected string
	}{
		{
			name: "Initializing while placement intent is unset",
			spec: coreapi.ServiceProviderClusterSpec{},
			expected: phaseHeader + fmt.Sprintf(`backend_cluster_phase_info{phase="Initializing",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
		},
		{
			name: "Scheduled once placement intent is set",
			spec: coreapi.ServiceProviderClusterSpec{ManagementClusterResourceID: mcResourceID},
			expected: phaseHeader + fmt.Sprintf(`backend_cluster_phase_info{phase="Scheduled",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID)),
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
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(tc.expected), "backend_cluster_phase_info"))
		})
	}
}

func TestClusterInfoMetricsHandler_PhaseInfoTransitionClearsStaleSeries(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))
	mcResourceID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)

	// First unplaced (Initializing).
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
	})
	// Then placed (Scheduled): the stale Initializing series must be cleared so only
	// the current phase remains.
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
		Spec:           coreapi.ServiceProviderClusterSpec{ManagementClusterResourceID: mcResourceID},
	})

	expected := fmt.Sprintf(`# HELP backend_cluster_phase_info Current internal lifecycle phase of the cluster. Value is always 1. Phase: Initializing, Scheduled
# TYPE backend_cluster_phase_info gauge
backend_cluster_phase_info{phase="Scheduled",resource_id="%s",subscription_id="%s"} 1
`, resourceIDMetricLabel(clusterResourceID), subscriptionIDMetricLabel(clusterResourceID))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_cluster_phase_info"))
}

func TestClusterInfoMetricsHandler_PhaseInfoDeletedOnDelete(t *testing.T) {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/serviceProviderClusters/default"))

	reg := prometheus.NewRegistry()
	handler := NewClusterInfoMetricsHandler(reg)
	handler.Sync(context.Background(), &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
	})
	handler.Delete(strings.ToLower(spcResourceID.String()))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_cluster_phase_info"))
}
