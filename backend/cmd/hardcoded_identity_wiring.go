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
	"context"
	"fmt"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/certificate"
)

func newHardcodedIdentity(ctx context.Context, azureMIMockCertBundlePath string, azureMIMockClientID string, azureMIMockPrincipalID string, azureMIMockTenantID string) (*azureclient.HardcodedIdentity, error) {
	certReader, err := certificate.NewWatchingAzureIdentityFileReader(ctx, azureMIMockCertBundlePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate reader: %w", err)
	}
	if err := certReader.Run(ctx, 1*time.Minute); err != nil {
		return nil, fmt.Errorf("failed to run certificate reader: %w", err)
	}

	return &azureclient.HardcodedIdentity{
		ClientID:                azureMIMockClientID,
		CertificateBundleReader: certReader,
		PrincipalID:             azureMIMockPrincipalID,
		TenantID:                azureMIMockTenantID,
	}, nil
}
