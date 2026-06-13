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

package clustersserviceregistration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

func validManagementCluster() *fleet.ManagementCluster {
	return &fleet.ManagementCluster{
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"),
			PublicDNSZoneResourceID:                              mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			MaestroConsumerName:                                  "consumer-1",
			MaestroRESTAPIURL:                                    "http://maestro:8000",
			MaestroGRPCTarget:                                    "maestro:8090",
		},
	}
}

func mustParseResourceID(s string) *azcorearm.ResourceID {
	rid, err := azcorearm.ParseResourceID(s)
	if err != nil {
		panic(err)
	}
	return rid
}

func TestBuildProvisionShardForCreate(t *testing.T) {
	tests := []struct {
		name            string
		modify          func(managementCluster *fleet.ManagementCluster)
		region          string
		wantErrContains string
	}{
		{
			name:   "valid input succeeds",
			region: "westus3",
		},
		{
			name:            "nil AKSResourceID",
			modify:          func(managementCluster *fleet.ManagementCluster) { managementCluster.Status.AKSResourceID = nil },
			region:          "westus3",
			wantErrContains: "AKSResourceID is required",
		},
		{
			name: "nil PublicDNSZoneResourceID",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.PublicDNSZoneResourceID = nil
			},
			region:          "westus3",
			wantErrContains: "PublicDNSZoneResourceID is required",
		},
		{
			name: "empty HostedClustersSecretsKeyVaultURL",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.HostedClustersSecretsKeyVaultURL = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersSecretsKeyVaultURL is required",
		},
		{
			name: "empty HostedClustersManagedIdentitiesKeyVaultURL",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersManagedIdentitiesKeyVaultURL is required",
		},
		{
			name: "empty HostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersSecretsKeyVaultManagedIdentityClientID is required",
		},
		{
			name: "empty MaestroConsumerName",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.MaestroConsumerName = ""
			},
			region:          "westus3",
			wantErrContains: "MaestroConsumerName is required",
		},
		{
			name: "empty MaestroRESTAPIURL",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.MaestroRESTAPIURL = ""
			},
			region:          "westus3",
			wantErrContains: "MaestroRESTAPIURL is required",
		},
		{
			name: "empty MaestroGRPCTarget",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Status.MaestroGRPCTarget = ""
			},
			region:          "westus3",
			wantErrContains: "MaestroGRPCTarget is required",
		},
		{
			name:            "empty region",
			region:          "",
			wantErrContains: "region is required",
		},
		{
			name: "unknown scheduling policy",
			modify: func(managementCluster *fleet.ManagementCluster) {
				managementCluster.Spec.SchedulingPolicy = "bogus"
			},
			region:          "westus3",
			wantErrContains: "unknown scheduling policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managementCluster := validManagementCluster()
			if tt.modify != nil {
				tt.modify(managementCluster)
			}

			builder, err := buildProvisionShardForCreate(managementCluster, tt.region)

			if len(tt.wantErrContains) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				assert.Nil(t, builder)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, builder)
		})
	}
}
