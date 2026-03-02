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
	"encoding/base64"
	"fmt"
	"os"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

func NewFirstPartyApplicationTokenCredentialRetriever(
	ctx context.Context, fpaCertBundlePath string,
	fpaClientID string, azureConfig *azureconfig.AzureConfig,
) (fpa.FirstPartyApplicationTokenCredentialRetriever, error) {
	if len(fpaCertBundlePath) == 0 || len(fpaClientID) == 0 {
		return nil, nil
	}

	// Create FPA TokenCredentials with watching
	certReader, err := fpa.NewWatchingFileCertificateReader(
		ctx,
		fpaCertBundlePath,
		1*time.Minute,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate reader: %w", err)
	}

	fpaTokenCredRetriever, err := fpa.NewFirstPartyApplicationTokenCredentialRetriever(
		fpaClientID,
		certReader,
		*azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA token credential retriever: %w", err)
	}

	return fpaTokenCredRetriever, nil
}

func NewFirstPartyApplicationClientBuilder(fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, azureConfig *azureconfig.AzureConfig) (azureclient.FirstPartyApplicationClientBuilder, error) {
	fpaClientBuilder := azureclient.NewFirstPartyApplicationClientBuilder(
		fpaTokenCredRetriever, azureConfig.CloudEnvironment.ARMClientOptions(),
	)

	return fpaClientBuilder, nil
}

func NewFirstPartyApplicationManagedIdentitiesDataplaneClientBuilder(
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	azureMIMockCertBundlePath string, azureMIMockClientID string, azureMIMockPrincipalID string, azureMIMockTenantID string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.FPAMIDataplaneClientBuilder, error) {
	if len(azureMIMockCertBundlePath) == 0 || len(azureMIMockClientID) == 0 || len(azureMIMockPrincipalID) == 0 || len(azureMIMockTenantID) == 0 {
		// TODO this can be improved at some point to support detecting when
		// the cert bundle path content changes. We could use a file watcher similar
		// to the one used in the fpa token credential retriever, and pass the retriever
		// to the client builder.
		bundle, err := os.ReadFile(azureMIMockCertBundlePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read bundle file: %w", err)
		}
		bundleBase64Encoded := base64.StdEncoding.EncodeToString(bundle)
		hardcodedIdentity := &azureclient.HardcodedIdentity{
			ClientID:     azureMIMockClientID,
			ClientSecret: bundleBase64Encoded,
			PrincipalID:  azureMIMockPrincipalID,
			TenantID:     azureMIMockTenantID,
		}
		hardcodedIdentityFPAMIDataplaneClientBuilder := azureclient.NewHardcodedIdentityFPAMIDataplaneClientBuilder(
			azureConfig.CloudEnvironment.CloudConfiguration(),
			hardcodedIdentity,
		)
		return hardcodedIdentityFPAMIDataplaneClientBuilder, nil
	}

	fpaMIdataplaneClientBuilder := azureclient.NewFPAMIDataplaneClientBuilder(
		azureConfig.AzureRuntimeConfig.ServiceTenantID,
		fpaTokenCredRetriever,
		azureConfig.AzureRuntimeConfig.ManagedIdentitiesDataPlaneAudienceResource,
		azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)

	return fpaMIdataplaneClientBuilder, nil
}

func NewServiceManagedIdentityClientBuilderFactory(
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
	azureConfig *azureconfig.AzureConfig,
) azureclient.ServiceManagedIdentityClientBuilderFactory {
	return azureclient.NewServiceManagedIdentityClientBuilderFactory(
		fpaMIdataplaneClientBuilder,
		azureConfig.CloudEnvironment.ARMClientOptions(),
	)
}
