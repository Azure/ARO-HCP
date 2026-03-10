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

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

// NewCheckAccessV2Client creates a new Check Access V2 client builder.
// If armHelperIdentityTokenCredentialRetriever is not nil, the ARM Helper Identity will be used to create the Check Access V2 client. Otherwise
// the FPA identity will be used to create the Check Access V2 client.
func NewCheckAccessV2ClientBuilder(ctx context.Context, armHelperIdentityTokenCredentialRetriever azureclient.ARMHelperIdentityTokenCredentialRetriever,
	fpaIdentityTokenCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, azureConfig *azureconfig.AzureConfig, azureLocation string,
) azureclient.CheckAccessV2ClientBuilder {

	checkAccessV2Endpoint := azureConfig.CloudEnvironment.CheckAccessV2Endpoint(azureLocation)
	checkAccessV2Scope := azureConfig.CloudEnvironment.CheckAccessV2Scope()

	// In aro-hcp environments where we don't have a real FPA, we use the ARM Helper identity to create the Check Access V2 client
	// In aro-hcp environments where we have a real FPA, we use the FPA identity to create the Check Access V2 client
	if armHelperIdentityTokenCredentialRetriever != nil {
		return azureclient.NewArmHelperIdentityCheckAccessV2ClientBuilder(armHelperIdentityTokenCredentialRetriever, checkAccessV2Endpoint, checkAccessV2Scope, azureConfig.CloudEnvironment.AZCoreClientOptions())
	}

	return azureclient.NewRealFPAIdentityCheckAccessV2ClientBuilder(fpaIdentityTokenCredentialRetriever, checkAccessV2Endpoint, checkAccessV2Scope, azureConfig.CloudEnvironment.AZCoreClientOptions())
}
