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

package azureapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001preview"
)

func TestClusterKeyVaultTypePreservation(t *testing.T) {
	registry := coreapi.NewAPIRegistry()
	for _, register := range []func(coreapi.APIRegistry) error{
		v20240610preview.RegisterVersion,
		v20251223preview.RegisterVersion,
		v20260630preview.RegisterVersion,
		v20260901preview.RegisterVersion,
		v20261001preview.RegisterVersion,
	} {
		require.NoError(t, register(registry))
	}

	for _, versionName := range registry.ListVersions().UnsortedList() {
		t.Run(versionName, func(t *testing.T) {
			version, ok := registry.Lookup(versionName)
			require.True(t, ok)

			// Build an existing cluster persisted with a Managed HSM key vault type.
			// keyVaultType only exists in the external API of v20261001preview and
			// newer; older versions must still preserve it on update.
			existing, err := version.NewHCPOpenShiftCluster(nil).ConvertToInternal(nil)
			require.NoError(t, err)
			existing.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
			existing.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
				EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
				Kms: &coreapi.KmsEncryptionProfile{
					Visibility:   metadataapi.KeyVaultVisibilityPublic,
					ActiveKey:    coreapi.KmsKey{Name: "test-key", VaultName: "test-vault", Version: "test-version"},
					KeyVaultType: coreapi.KmsKeyVaultTypeManagedHSM,
				},
			}

			// Send an update through this API version that does not touch etcd.
			external := version.NewHCPOpenShiftCluster(existing)
			body := `{"properties":{"platform":{"operatorsAuthentication":{"userAssignedIdentities":{}}}}}`
			require.NoError(t, json.Unmarshal([]byte(body), external))

			cluster, err := external.ConvertToInternal(existing)
			require.NoError(t, err)

			require.NotNil(t, cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged,
				"customerManaged must be preserved on update")
			require.NotNil(t, cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms,
				"kms must be preserved on update")
			require.Equal(t, coreapi.KmsKeyVaultTypeManagedHSM,
				cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType,
				"all API versions must preserve keyVaultType on update")
		})
	}
}
