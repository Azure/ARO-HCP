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

// Package common holds the option handling shared by the entra-app-credentials
// subcommands.
package common

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// ParseVaultURL validates a Key Vault data-plane base URL and returns it normalized.
func ParseVaultURL(raw string) (string, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.Path != "" {
		return "", fmt.Errorf("--vault-url must be an HTTPS Key Vault base URL without a path")
	}
	return parsedURL.String(), nil
}

// NewCredential creates the token credential used for Key Vault and Microsoft
// Graph. AZURE_TOKEN_CREDENTIALS must select the token credentials to try.
func NewCredential() (azcore.TokenCredential, error) {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	return credential, nil
}
