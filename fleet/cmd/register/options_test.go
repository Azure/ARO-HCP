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

package register

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validRawOptions() *RawRegisterOptions {
	return &RawRegisterOptions{
		CloudEnvironment:                 "AzurePublicCloud",
		CosmosURL:                        "https://cosmos.example.com",
		CosmosName:                       "testdb",
		StampIdentifier:                  "1",
		SchedulingPolicy:                 "Schedulable",
		AKSResourceID:                    "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1",
		PublicDNSZoneResourceID:          "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com",
		HostedClustersSecretsKeyVaultURL: "https://kv.vault.azure.net",
		HostedClustersManagedIdentitiesKeyVaultURL:           "https://mi-kv.vault.azure.net",
		HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
		MaestroConsumerName:                                  "consumer-1",
		MaestroRESTAPIURL:                                    "http://maestro:8000",
		MaestroGRPCTarget:                                    "maestro:8090",
		KubeApplierCosmosContainerName:                       "Manifests-MC-1",
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modify    func(opts *RawRegisterOptions)
		expectErr string
	}{
		{
			name:   "valid options pass validation",
			modify: func(opts *RawRegisterOptions) {},
		},
		{
			name:      "invalid cloud environment",
			modify:    func(opts *RawRegisterOptions) { opts.CloudEnvironment = "InvalidCloud" },
			expectErr: "--cloud-environment",
		},
		{
			name:      "empty stamp identifier",
			modify:    func(opts *RawRegisterOptions) { opts.StampIdentifier = "" },
			expectErr: "invalid stamp identifier",
		},
		{
			name:      "invalid scheduling policy",
			modify:    func(opts *RawRegisterOptions) { opts.SchedulingPolicy = "InvalidPolicy" },
			expectErr: "invalid scheduling policy",
		},
		{
			name:      "invalid AKS resource ID",
			modify:    func(opts *RawRegisterOptions) { opts.AKSResourceID = "not-a-resource-id" },
			expectErr: "invalid aks-resource-id",
		},
		{
			name:      "invalid public DNS zone resource ID",
			modify:    func(opts *RawRegisterOptions) { opts.PublicDNSZoneResourceID = "not-a-resource-id" },
			expectErr: "invalid public-dns-zone-resource-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := validRawOptions()
			tt.modify(opts)

			validated, err := opts.Validate(t.Context())

			if len(tt.expectErr) > 0 {
				require.ErrorContains(t, err, tt.expectErr)
				require.Nil(t, validated)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, validated)
		})
	}
}
