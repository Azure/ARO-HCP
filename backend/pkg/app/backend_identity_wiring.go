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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewBackendIdentityClientBuilder creates a new BackendIdentityClientBuilder that
// builds Azure clients that interact with the Azure platform as the
// backend identity. The backend identity is used to interact with Red Hat side Azure infrastructure.
func NewBackendIdentityClientBuilder(ctx context.Context, azureConfig *azureconfig.AzureConfig) (azureclient.BackendIdentityClientBuilder, error) {
	// Backend's identity uses the DefaultAzureCredential.
	// See https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains#defaultazurecredential-overview
	// for more details on it.
	defaultAzureCredential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions:                *azureConfig.CloudEnvironment.AZCoreClientOptions(),
			RequireAzureTokenCredentials: true,
		},
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create backend identity Azure credential: %w", err))
	}

	return azureclient.NewBackendIdentityClientBuilder(
		defaultAzureCredential,
		azureConfig.CloudEnvironment.AZCoreClientOptions(),
		azureConfig.CloudEnvironment.ARMClientOptions(),
		azureConfig.AzureRuntimeConfig.DataPlaneIdentitiesOIDCConfiguration.StorageAccountBlobServiceURL,
	), nil
}

func NewBackendIdentityAzureCachedReaders(ctx context.Context, backendIdentityClientBuilder azureclient.BackendIdentityClientBuilder) (*cachedreader.BackendIdentityAzureCachedReaders, error) {
	roleDefinitionsClient, err := backendIdentityClientBuilder.RoleDefinitionsClient()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create role definitions client: %w", err))
	}

	roleDefinitionsCachedReader := cachedreader.NewRoleDefinitionsCachedReader(
		roleDefinitionsClient,
	)

	cachedReaders := &cachedreader.BackendIdentityAzureCachedReaders{
		RoleDefinitionsCachedReader: roleDefinitionsCachedReader,
	}

	return cachedReaders, nil
}
