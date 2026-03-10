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

// NewARMHelperIdentityTokenCredentialRetriever creates a new ARM Helper identity token credential retriever.
// If the ARM Helper identity armHelperCertBundlePath and armHelperClientID attributes are not set it returns nil.
func NewARMHelperIdentityTokenCredentialRetriever(ctx context.Context, armHelperTenantID string, armHelperClientID string, armHelperCertBundlePath string, azureConfig *azureconfig.AzureConfig) (azureclient.ARMHelperIdentityTokenCredentialRetriever, error) {
	if len(armHelperTenantID) == 0 || len(armHelperClientID) == 0 || len(armHelperCertBundlePath) == 0 {
		return nil, nil
	}

	certReader, err := certificate.NewWatchingAzureIdentityFileReader(ctx, armHelperCertBundlePath, 1*time.Minute)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create certificate reader: %w", err))
	}

	retriever, err := azureclient.NewARMHelperIdentityTokenCredentialRetriever(armHelperTenantID, armHelperClientID, certReader, azureConfig.CloudEnvironment.AZCoreClientOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create ARM Helper identity token credential retriever: %w", err))
	}

	return retriever, nil
}
