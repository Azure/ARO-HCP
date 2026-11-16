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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newBackendDefaultAzureCredential(azureConfig *azureconfig.AzureConfig) (azcore.TokenCredential, error) {
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
	return defaultAzureCredential, nil
}
