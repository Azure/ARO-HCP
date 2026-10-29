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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func validManagementCluster() *fleetapi.ManagementCluster {
	return &fleetapi.ManagementCluster{
		Spec: fleetapi.ManagementClusterSpec{
			SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleetapi.ManagementClusterStatus{
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

func TestProvisionShardStatusUpdatePreservesLegacyMaestroConfig(t *testing.T) {
	shard, err := arohcpv1alpha1.NewProvisionShard().
		Status(ocm.CSProvisionShardStatusMaintenance).
		MaestroConfig(arohcpv1alpha1.NewProvisionShardMaestroConfig().ConsumerName("legacy-consumer")).
		Build()
	require.NoError(t, err)
	builder, err := provisionShardStatusUpdateBuilder(shard, fleetapi.ManagementClusterSchedulingPolicySchedulable)
	require.NoError(t, err)
	require.NotNil(t, builder)
	patch, err := builder.Build()
	require.NoError(t, err)
	assert.Equal(t, ocm.CSProvisionShardStatusActive, patch.Status())
	_, hasMaestroConfig := patch.GetMaestroConfig()
	assert.False(t, hasMaestroConfig, "status update must leave legacy Maestro configuration untouched")
	assert.Equal(t, "legacy-consumer", shard.MaestroConfig().ConsumerName())
}

func TestBuildProvisionShardForCreate(t *testing.T) {
	tests := []struct {
		name            string
		modify          func(managementCluster *fleetapi.ManagementCluster)
		region          string
		wantErrContains string
	}{
		{
			name:   "valid input succeeds without Maestro configuration",
			region: "westus3",
		},
		{
			name:            "nil AKSResourceID",
			modify:          func(managementCluster *fleetapi.ManagementCluster) { managementCluster.Status.AKSResourceID = nil },
			region:          "westus3",
			wantErrContains: "AKSResourceID is required",
		},
		{
			name: "nil PublicDNSZoneResourceID",
			modify: func(managementCluster *fleetapi.ManagementCluster) {
				managementCluster.Status.PublicDNSZoneResourceID = nil
			},
			region:          "westus3",
			wantErrContains: "PublicDNSZoneResourceID is required",
		},
		{
			name: "empty HostedClustersSecretsKeyVaultURL",
			modify: func(managementCluster *fleetapi.ManagementCluster) {
				managementCluster.Status.HostedClustersSecretsKeyVaultURL = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersSecretsKeyVaultURL is required",
		},
		{
			name: "empty HostedClustersManagedIdentitiesKeyVaultURL",
			modify: func(managementCluster *fleetapi.ManagementCluster) {
				managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersManagedIdentitiesKeyVaultURL is required",
		},
		{
			name: "empty HostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(managementCluster *fleetapi.ManagementCluster) {
				managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = ""
			},
			region:          "westus3",
			wantErrContains: "HostedClustersSecretsKeyVaultManagedIdentityClientID is required",
		},

		{
			name:            "empty region",
			region:          "",
			wantErrContains: "region is required",
		},
		{
			name: "unknown scheduling policy",
			modify: func(managementCluster *fleetapi.ManagementCluster) {
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
			require.NotNil(t, builder)
			shard, err := builder.Build()
			require.NoError(t, err)
			assert.Nil(t, shard.MaestroConfig())
			assert.Equal(t, managementCluster.Status.AKSResourceID.String(), shard.AzureShard().AksManagementClusterResourceId())
		})
	}
}
