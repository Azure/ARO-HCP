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
	certReader, err := certificate.NewWatchingAzureIdentityFileReader(
		ctx,
		fpaCertBundlePath,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate reader: %w", err)
	}
	err = certReader.Run(ctx, 1*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to run certificate reader: %w", err)
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

func NewServiceManagedIdentityClientBuilder(fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder, azureConfig *azureconfig.AzureConfig) azureclient.ServiceManagedIdentityClientBuilder {
	return azureclient.NewServiceManagedIdentityClientBuilder(
		fpaMIdataplaneClientBuilder,
		azureConfig.CloudEnvironment.ARMClientOptions(),
	)
}

func NewClusterOperatorIdentityClientBuilder(fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder, azureConfig *azureconfig.AzureConfig) azureclient.ClusterOperatorIdentityClientBuilder {
	return azureclient.NewClusterOperatorIdentityClientBuilder(
		fpaMIdataplaneClientBuilder,
		azureConfig.CloudEnvironment.ARMClientOptions(),
	)
}
