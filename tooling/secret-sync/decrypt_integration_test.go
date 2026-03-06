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

//go:build decrypttest

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/ARO-Tools/tools/secret-sync/config"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/golang-jwt/jwt/v5"
	"sigs.k8s.io/yaml"
)

// registryAuth represents authentication credentials for a single registry.
type registryAuth struct {
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth"`
}

// dockerConfigJSON represents a Docker auth config (pull secrets).
type dockerConfigJSON struct {
	Auths map[string]registryAuth `json:"auths"`
}

// secretValidator holds a validation function and whether the secret is base64-encoded.
type secretValidator struct {
	validate      func([]byte) error
	base64Encoded bool
}

var secretValidators = map[string]secretValidator{
	"acm-d-password":             {validate: validateNonEmptyString},
	"acm-d-username":             {validate: validateNonEmptyString},
	"component-sync-pull-secret": {validate: validatePullSecret, base64Encoded: true},
	"ocmirror-pull-secret":       {validate: validatePullSecret, base64Encoded: true},
	"prow-token":                 {validate: validateJWT},
	"quay-io-bearer":             {validate: validateNonEmptyString},
	"quay-password":              {validate: validateNonEmptyString},
	"quay-username":              {validate: validateNonEmptyString},
}

func validatePullSecret(data []byte) error {
	var cfg dockerConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal as Docker config JSON: %w", err)
	}
	if len(cfg.Auths) == 0 {
		return fmt.Errorf("Docker config has no auths entries")
	}
	return nil
}

func validateJWT(data []byte) error {
	_, _, err := jwt.NewParser().ParseUnverified(strings.TrimSpace(string(data)), jwt.MapClaims{})
	if err != nil {
		return fmt.Errorf("failed to parse as JWT: %w", err)
	}
	return nil
}

func validateNonEmptyString(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("expected non-empty string")
	}
	return nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func TestDecryptAndValidateSecrets(t *testing.T) {
	keyVaultURI := envOrDefault("KEY_VAULT_URI", "https://arohcpdev-global.vault.azure.net")
	keyEncryptionKeyName := envOrDefault("KEY_ENCRYPTION_KEY_NAME", "secretSyncKey")

	// ENCRYPTED_SECRETS_CONFIG holds the YAML content directly.
	content := os.Getenv("ENCRYPTED_SECRETS_CONFIG")
	if content == "" {
		t.Fatal("ENCRYPTED_SECRETS_CONFIG environment variable is not set")
	}

	var cfg config.SecretSync
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("Failed to parse ENCRYPTED_SECRETS_CONFIG: %v", err)
	}

	kv, ok := cfg.KeyVaults[keyVaultURI]
	if !ok {
		t.Fatalf("Key vault %s not found in config. Available vaults: %v", keyVaultURI, slices.Collect(maps.Keys(cfg.KeyVaults)))
	}

	if len(kv.EncryptedSecrets) == 0 {
		t.Fatal("No encrypted secrets found in config")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("Failed to create Azure credential: %v", err)
	}

	keysClient, err := azkeys.NewClient(keyVaultURI, cred, nil)
	if err != nil {
		t.Fatalf("Failed to create Key Vault keys client: %v", err)
	}

	for name, secret := range kv.EncryptedSecrets {
		t.Run(name, func(t *testing.T) {
			// Decode base64-encoded ciphertext
			secretCiphertext, err := base64.StdEncoding.DecodeString(secret.Data)
			if err != nil {
				t.Fatalf("Failed to base64-decode secret data: %v", err)
			}

			dataEncryptionKeyCiphertext, err := base64.StdEncoding.DecodeString(secret.DataEncryptionKey)
			if err != nil {
				t.Fatalf("Failed to base64-decode data encryption key: %v", err)
			}

			// Unwrap the DEK using Key Vault RSA-OAEP-256
			dataEncryptionKey, err := keysClient.Decrypt(t.Context(), keyEncryptionKeyName, "",
				azkeys.KeyOperationParameters{
					Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
					Value:     dataEncryptionKeyCiphertext,
				},
				&azkeys.DecryptOptions{},
			)
			if err != nil {
				t.Fatalf("Failed to decrypt data encryption key: %v", err)
			}

			// Decrypt the secret data using AES-GCM
			block, err := aes.NewCipher(dataEncryptionKey.Result)
			if err != nil {
				t.Fatalf("Failed to create AES cipher: %v", err)
			}

			aesgcm, err := cipher.NewGCMWithRandomNonce(block)
			if err != nil {
				t.Fatalf("Failed to create GCM cipher: %v", err)
			}

			plaintext, err := aesgcm.Open(nil, nil, secretCiphertext, nil)
			if err != nil {
				t.Fatalf("Failed to decrypt secret data: %v", err)
			}

			if len(plaintext) == 0 {
				t.Fatal("Decrypted secret is empty")
			}

			// Look up the validator for this secret
			validator, ok := secretValidators[name]
			if !ok {
				t.Errorf("No validator defined for secret %q — add an entry to secretValidators", name)
				return
			}

			// Some secrets store base64-encoded content (e.g. Docker auth config JSON).
			// Decode them before validation.
			valueToValidate := plaintext
			if validator.base64Encoded {
				decoded, err := base64.StdEncoding.DecodeString(string(plaintext))
				if err != nil {
					t.Fatalf("Secret is expected to be base64-encoded but decoding failed: %v", err)
				}
				if len(decoded) == 0 {
					t.Fatal("Base64-decoded secret content is empty")
				}
				valueToValidate = decoded
			}

			if err := validator.validate(valueToValidate); err != nil {
				t.Errorf("Validation failed for secret %q: %v", name, err)
			}
		})
	}
}
