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

package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
)

// TestKeyVaultDNSSuffix pins the Key Vault data plane DNS suffix for every
// supported cloud. Getting one of these wrong sends a customer's etcd KMS key
// probe at a hostname that does not exist in that cloud.
func TestKeyVaultDNSSuffix(t *testing.T) {
	for _, tc := range []struct {
		cloudEnvironmentName apisconfigv1.CloudEnvironmentName
		expectedSuffix       string
	}{
		{apisconfigv1.AzurePublicCloud, "vault.azure.net"},
		{apisconfigv1.AzureUSGovernmentCloud, "vault.usgovcloudapi.net"},
		{apisconfigv1.AzureChinaCloud, "vault.azure.cn"},
	} {
		t.Run(string(tc.cloudEnvironmentName), func(t *testing.T) {
			cloudEnvironment, err := NewAzureCloudEnvironment(tc.cloudEnvironmentName, nil)
			require.NoError(t, err)

			require.Equal(t, tc.expectedSuffix, cloudEnvironment.KeyVaultDNSSuffix())
		})
	}
}
