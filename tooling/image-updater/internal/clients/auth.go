// Copyright 2025 Microsoft Corporation
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

package clients

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// KeyVaultConfig holds configuration for fetching pull secrets from Azure Key Vault
type KeyVaultConfig struct {
	VaultURL   string // e.g., "https://aro-hcp-dev-global-kv.vault.azure.net/"
	SecretName string // e.g., "component-sync-pull-secret"
}

// GetRemoteOptions returns remote options with Docker credentials if useAuth is true
// It uses the default Docker config file (~/.docker/config.json) for authentication
func GetRemoteOptions(useAuth bool) []remote.Option {
	if !useAuth {
		return nil
	}

	// Use the default keychain which reads from ~/.docker/config.json
	// This automatically handles authentication for registries the user has logged into
	return []remote.Option{
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	}
}

// FetchAndMergeKeyVaultPullSecret fetches a Docker pull secret from Azure Key Vault
// and merges it with the existing Docker config file
func FetchAndMergeKeyVaultPullSecret(ctx context.Context, kvConfig KeyVaultConfig) error {
	if kvConfig.VaultURL == "" || kvConfig.SecretName == "" {
		return fmt.Errorf("KeyVault URL and secret name must be provided")
	}

	// Create Azure credential using DefaultAzureCredential
	// This supports Azure CLI, managed identity, environment variables, etc.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Create Key Vault client
	client, err := azsecrets.NewClient(kvConfig.VaultURL, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create KeyVault client: %w", err)
	}

	// Fetch the secret
	resp, err := client.GetSecret(ctx, kvConfig.SecretName, "", nil)
	if err != nil {
		return fmt.Errorf("failed to get secret %s from KeyVault: %w", kvConfig.SecretName, err)
	}

	if resp.Value == nil {
		return fmt.Errorf("secret %s is empty", kvConfig.SecretName)
	}

	// The secret value should be a base64-encoded Docker config JSON
	secretValue := *resp.Value

	// Try to decode as base64 first (common format for pull secrets)
	var dockerConfigData []byte
	decoded, err := base64.StdEncoding.DecodeString(secretValue)
	if err == nil {
		// Successfully decoded as base64
		dockerConfigData = decoded
	} else {
		// Not base64, assume it's raw JSON
		dockerConfigData = []byte(secretValue)
	}

	// Parse the Docker config JSON
	var kvDockerConfig map[string]interface{}
	if err := json.Unmarshal(dockerConfigData, &kvDockerConfig); err != nil {
		return fmt.Errorf("failed to parse Docker config from KeyVault secret: %w", err)
	}

	// Merge with existing Docker config
	if err := mergeDockerConfig(kvDockerConfig); err != nil {
		return fmt.Errorf("failed to merge Docker config: %w", err)
	}

	return nil
}

// mergeDockerConfig merges a Docker config from KeyVault with the existing ~/.docker/config.json
func mergeDockerConfig(kvConfig map[string]interface{}) error {
	// Get Docker config path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	dockerConfigPath := filepath.Join(homeDir, ".docker", "config.json")

	// Read existing Docker config
	var existingConfig map[string]interface{}
	existingData, err := os.ReadFile(dockerConfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read Docker config: %w", err)
		}
		// Config doesn't exist, create a new one
		existingConfig = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(existingData, &existingConfig); err != nil {
			return fmt.Errorf("failed to parse existing Docker config: %w", err)
		}
	}

	// Merge auths section
	if kvAuths, ok := kvConfig["auths"].(map[string]interface{}); ok {
		if existingConfig["auths"] == nil {
			existingConfig["auths"] = make(map[string]interface{})
		}
		existingAuths := existingConfig["auths"].(map[string]interface{})

		for registry, auth := range kvAuths {
			existingAuths[registry] = auth
		}
	}

	// Write merged config back
	mergedData, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal merged Docker config: %w", err)
	}

	// Ensure .docker directory exists
	dockerDir := filepath.Dir(dockerConfigPath)
	if err := os.MkdirAll(dockerDir, 0700); err != nil {
		return fmt.Errorf("failed to create .docker directory: %w", err)
	}

	// Write the merged config
	if err := os.WriteFile(dockerConfigPath, mergedData, 0600); err != nil {
		return fmt.Errorf("failed to write Docker config: %w", err)
	}

	return nil
}
