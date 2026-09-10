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

package cmd

import (
	"encoding/base64"
	"fmt"
	"os"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
)

func newHardcodedIdentityFPAMIDataplaneClientBuilder(
	azureMIMockCertBundlePath string, azureMIMockClientID string, azureMIMockPrincipalID string, azureMIMockTenantID string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.FPAMIDataplaneClientBuilder, *azureclient.HardcodedIdentity, error) {
	bundle, err := os.ReadFile(azureMIMockCertBundlePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read bundle file: %w", err)
	}
	bundleBase64Encoded := base64.StdEncoding.EncodeToString(bundle)
	hardcodedIdentity := &azureclient.HardcodedIdentity{
		ClientID:     azureMIMockClientID,
		ClientSecret: bundleBase64Encoded,
		PrincipalID:  azureMIMockPrincipalID,
		TenantID:     azureMIMockTenantID,
	}
	return azureclient.NewHardcodedIdentityFPAMIDataplaneClientBuilder(azureConfig.CloudEnvironment.CloudConfiguration(), hardcodedIdentity), hardcodedIdentity, nil
}
