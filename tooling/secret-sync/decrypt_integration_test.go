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
	"os"
	"regexp"
	"testing"

	"github.com/Azure/ARO-Tools/tools/secret-sync/config"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

var secretValidationPatterns = map[string]*regexp.Regexp{
	"acm-d-password":             regexp.MustCompile(`.+`),
	"acm-d-username":             regexp.MustCompile(`.+`),
	"component-sync-pull-secret": regexp.MustCompile(`(?s)\{.*"auths"\s*:\s*\{.*\}\s*\}`),
	"ocmirror-pull-secret":       regexp.MustCompile(`(?s)\{.*"auths"\s*:\s*\{.*\}\s*\}`),
	"prow-token":                 regexp.MustCompile(`.+`),
	"quay-io-bearer":             regexp.MustCompile(`.+`),
	"quay-password":              regexp.MustCompile(`.+`),
	"quay-username":              regexp.MustCompile(`.+`),
}

// base64EncodedSecrets lists secrets whose decrypted plaintext is itself
// base64-encoded (e.g. Docker auth config JSON stored as base64).
// These are decoded an extra time before validation.
var base64EncodedSecrets = map[string]bool{
	"component-sync-pull-secret": true,
	"ocmirror-pull-secret":       true,
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestDecryptAndValidateSecrets(t *testing.T) {
	configFile := envOrDefault("ENCRYPTED_SECRETS_FILE", "../../dev-infrastructure/data/encryptedsecrets.yaml")
	keyVaultURI := envOrDefault("KEY_VAULT_URI", "https://arohcpdev-global.vault.azure.net")
	keyEncryptionKeyName := envOrDefault("KEY_ENCRYPTION_KEY_NAME", "secretSyncKey")

	cfg, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("Failed to load config from %s: %v", configFile, err)
	}

	kv, ok := cfg.KeyVaults[keyVaultURI]
	if !ok {
		t.Fatalf("Key vault %s not found in config. Available vaults: %v", keyVaultURI, keysOf(cfg.KeyVaults))
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

			// Some secrets store base64-encoded content (e.g. Docker auth config JSON).
			// Decode them before validation.
			valueToValidate := plaintext
			if base64EncodedSecrets[name] {
				decoded, err := base64.StdEncoding.DecodeString(string(plaintext))
				if err != nil {
					t.Fatalf("Secret is expected to be base64-encoded but decoding failed: %v", err)
				}
				if len(decoded) == 0 {
					t.Fatal("Base64-decoded secret content is empty")
				}
				valueToValidate = decoded
			}

			// Validate against expected pattern
			pattern, ok := secretValidationPatterns[name]
			if !ok {
				t.Errorf("No validation pattern defined for secret %q — add an entry to secretValidationPatterns", name)
				return
			}

			if !pattern.MatchString(string(valueToValidate)) {
				preview := string(valueToValidate)
				if len(preview) > 5 {
					preview = preview[:5] + "..."
				}
				t.Errorf("Decrypted secret does not match expected pattern %q; preview: %q", pattern.String(), preview)
			}
		})
	}
}
