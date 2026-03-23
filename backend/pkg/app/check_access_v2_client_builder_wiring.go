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

package app

import (
	"context"
	"fmt"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewCheckAccessV2Client creates a new Check Access V2 client builder.
// If the azureARMPermissionsManager parameters are not empty the Azure Permissions Manager Identity will be used to create the Check Access V2 client.
// Otherwise the FPA identity will be used to create the Check Access V2 client.
func NewCheckAccessV2ClientBuilder(ctx context.Context, fpaIdentityTokenCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	azureARMPermissionsManagerTenantID string, azureARMPermissionsManagerClientID string, azureARMPermissionsManagerCertBundlePath string,
	azureConfig *azureconfig.AzureConfig, azureLocation string,
) (azureclient.CheckAccessV2ClientBuilder, error) {
	checkAccessV2Endpoint := azureConfig.CloudEnvironment.CheckAccessV2Endpoint(azureLocation)
	checkAccessV2Scope := azureConfig.CloudEnvironment.CheckAccessV2Scope()

	// In ARO-HCP environments where we don't have a real FPA, we use the Azure Permissions Manager identity to create the Check Access V2 client
	if len(azureARMPermissionsManagerTenantID) > 0 && len(azureARMPermissionsManagerClientID) > 0 && len(azureARMPermissionsManagerCertBundlePath) > 0 {
		azureARMPermissionsManagerIdentityTokenCredentialRetriever, err := newAzurePermissionsManagerIdentityTokenCredentialRetriever(
			ctx, azureARMPermissionsManagerTenantID, azureARMPermissionsManagerClientID,
			azureARMPermissionsManagerCertBundlePath, azureConfig,
		)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create Azure Permissions Manager identity token credential retriever: %w", err))
		}
		return azureclient.NewArmPermissionsManagerIdentityCheckAccessV2ClientBuilder(azureARMPermissionsManagerIdentityTokenCredentialRetriever, checkAccessV2Endpoint, checkAccessV2Scope, azureConfig.CloudEnvironment.AZCoreClientOptions()), nil
	}

	// In ARO-HCP environments where we have a real FPA, we use the FPA identity to create the Check Access V2 client
	return azureclient.NewRealFPAIdentityCheckAccessV2ClientBuilder(fpaIdentityTokenCredentialRetriever, checkAccessV2Endpoint, checkAccessV2Scope, azureConfig.CloudEnvironment.AZCoreClientOptions()), nil
}
