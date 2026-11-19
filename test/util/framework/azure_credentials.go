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

package framework

import (
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// NewAzureCredential shares the framework's authentication policy with operator
// commands without constructing a test context. An explicit AZURE_TOKEN_CREDENTIALS
// selection takes precedence over development defaults. Only CLI authentication
// uses subscriptionID, to select the same local account for every SDK client.
func NewAzureCredential(development bool, subscriptionID string) (azcore.TokenCredential, error) {
	selected := os.Getenv("AZURE_TOKEN_CREDENTIALS")
	if strings.EqualFold(selected, "AzureCLICredential") {
		credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{Subscription: subscriptionID})
		if err != nil {
			return nil, fmt.Errorf("build selected CLI credential: %w", err)
		}
		return credential, nil
	}
	if selected != "" || !development {
		credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		if err != nil {
			return nil, fmt.Errorf("build selected Azure credential: %w", err)
		}
		return credential, nil
	}
	// Development CI already supplies these values. Prefer pure-Go auth rather
	// than spawning an Azure CLI process for token acquisition under parallel load.
	clientID, clientSecret, tenantID := os.Getenv("AZURE_CLIENT_ID"), os.Getenv("AZURE_CLIENT_SECRET"), os.Getenv("AZURE_TENANT_ID")
	if clientID != "" && clientSecret != "" && tenantID != "" {
		credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("build development client secret credential: %w", err)
		}
		return credential, nil
	}
	credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{Subscription: subscriptionID})
	if err != nil {
		return nil, fmt.Errorf("build development CLI credential: %w", err)
	}
	return credential, nil
}
