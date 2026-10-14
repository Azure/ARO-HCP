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

package frontend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	v20261001 "github.com/Azure/ARO-HCP/internal/azureapi/v20261001"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDecodeDesiredClusterReplacePreservesKeyVaultType(t *testing.T) {
	registry := coreapi.NewAPIRegistry()
	require.NoError(t, v20261001.RegisterVersion(registry))
	versionName := registry.ListVersions().UnsortedList()[0]
	version, ok := registry.Lookup(versionName)
	require.True(t, ok)

	resourceID, err := azcorearm.ParseResourceID("/subscriptions/11111111-2222-4333-8444-555555555555/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster")
	require.NoError(t, err)

	oldInternalCluster, err := version.NewCluster(nil).ConvertToInternal(nil)
	require.NoError(t, err)
	oldInternalCluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
	oldInternalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
		EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
		Kms: &coreapi.KmsEncryptionProfile{
			Visibility:   metadataapi.KeyVaultVisibilityPublic,
			ActiveKey:    coreapi.KmsKey{Name: "test-key", VaultName: "test-vault", Version: "test-version"},
			KeyVaultType: coreapi.KmsKeyVaultTypeManagedHSM,
		},
	}

	// A PUT that resends the KMS block for key rotation but omits the
	// read/create-only keyVaultType, which the ARM contract allows.
	body := `{"properties":{"etcd":{"dataEncryption":{"keyManagementMode":"CustomerManaged","customerManaged":{"encryptionType":"KMS","kms":{"vaultName":"test-vault","visibility":"Public","activeKey":{"name":"test-key","version":"test-version-2"}}}}}}}`

	created, modified := time.Unix(1600000000, 0).UTC(), time.Unix(1600000001, 0).UTC()
	ctx := utils.ContextWithResourceID(context.Background(), resourceID)
	ctx = ContextWithVersion(ctx, version)
	ctx = ContextWithBody(ctx, []byte(body))
	ctx = ContextWithSystemData(ctx, &coreapi.SystemData{CreatedAt: &created, LastModifiedAt: &modified})

	newInternalCluster, err := decodeDesiredClusterReplace(ctx, oldInternalCluster)
	require.NoError(t, err)

	require.NotNil(t, newInternalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged)
	require.NotNil(t, newInternalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms)
	require.Equal(t, coreapi.KmsKeyVaultTypeManagedHSM, newInternalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType,
		"PUT that omits keyVaultType must retain the stored value")
}
