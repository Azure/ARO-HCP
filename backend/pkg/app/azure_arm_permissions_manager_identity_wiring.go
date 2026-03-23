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
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/certificate"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewAzurePermissionsManagerIdentityTokenCredentialRetriever creates a new Azure Permissions Manager identity token credential retriever.
func newAzurePermissionsManagerIdentityTokenCredentialRetriever(ctx context.Context,
	azureARMPermissionsManagerTenantID string, azureARMPermissionsManagerClientID string, azureARMPermissionsManagerCertBundlePath string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.ARMPermissionsManagerIdentityTokenCredentialRetriever, error) {
	certReader, err := certificate.NewWatchingAzureIdentityFileReader(ctx, azureARMPermissionsManagerCertBundlePath)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create certificate reader: %w", err))
	}
	err = certReader.Run(ctx, 1*time.Minute)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to run certificate reader: %w", err))
	}

	retriever, err := azureclient.NewARMPermissionsManagerIdentityTokenCredentialRetriever(azureARMPermissionsManagerTenantID, azureARMPermissionsManagerClientID, certReader, azureConfig.CloudEnvironment.AZCoreClientOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Azure Permissions Manager identity token credential retriever: %w", err))
	}

	return retriever, nil
}
