// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package backups

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestNeedsWork(t *testing.T) {
	makeCluster := func() coreapi.HCPOpenShiftCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster",
		))
		return coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				BillingDocumentCosmosID: "billing-doc-123",
			},
		}
	}

	makeServiceProviderCluster := func() coreapi.ServiceProviderCluster {
		return coreapi.ServiceProviderCluster{
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1")),
				ControlPlaneNamespace:       "cp-ns",
				HostedClusterNamespace:      "hc-ns",
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*coreapi.HCPOpenShiftCluster, *coreapi.ServiceProviderCluster)
		want   bool
	}{
		{
			name:   "all fields populated returns true",
			mutate: func(_ *coreapi.HCPOpenShiftCluster, _ *coreapi.ServiceProviderCluster) {},
			want:   true,
		},
		{
			name: "DeletionTimestamp set returns false",
			mutate: func(c *coreapi.HCPOpenShiftCluster, _ *coreapi.ServiceProviderCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{}
			},
			want: false,
		},
		{
			name: "empty BillingDocumentCosmosID returns false",
			mutate: func(c *coreapi.HCPOpenShiftCluster, _ *coreapi.ServiceProviderCluster) {
				c.ServiceProviderProperties.BillingDocumentCosmosID = ""
			},
			want: false,
		},
		{
			name: "nil cluster ResourceID returns false",
			mutate: func(c *coreapi.HCPOpenShiftCluster, _ *coreapi.ServiceProviderCluster) {
				c.ResourceID = nil
			},
			want: false,
		},
		{
			name: "nil ManagementClusterResourceID returns false",
			mutate: func(_ *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				serviceProviderCluster.Status.ManagementClusterResourceID = nil
			},
			want: false,
		},
		{
			name: "empty ControlPlaneNamespace returns false",
			mutate: func(_ *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				serviceProviderCluster.Status.ControlPlaneNamespace = ""
			},
			want: false,
		},
		{
			name: "empty HostedClusterNamespace returns false",
			mutate: func(_ *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				serviceProviderCluster.Status.HostedClusterNamespace = ""
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := makeCluster()
			serviceProviderCluster := makeServiceProviderCluster()
			tt.mutate(&cluster, &serviceProviderCluster)
			assert.Equal(t, tt.want, needsWork(cluster, serviceProviderCluster))
		})
	}
}
