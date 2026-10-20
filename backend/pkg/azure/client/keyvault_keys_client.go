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

package client

//go:generate $MOCKGEN -typed -source=keyvault_keys_client.go -destination=mock_keyvault_keys_client.go -package client KeyVaultKeysClient,KeyVaultKeysClientBuilder

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// KeyVaultKeysClient interacts with the keys endpoints of an Azure Key Vault's
// data plane.
type KeyVaultKeysClient interface {
	// GetKey retrieves a single key version from the vault this client is bound to.
	// A nil or empty version selects the vault's current version of the key.
	GetKey(ctx context.Context, name string, version string, options *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error)
}

var _ KeyVaultKeysClient = (*azkeys.Client)(nil)

// KeyVaultKeysClientBuilder builds KeyVaultKeysClient instances bound to a
// specific Azure Key Vault and authenticating as a specific identity.
//
// Key Vault's data plane is addressed by DNS name alone, so no Azure resource
// ID for the vault is required. This matters because the cluster payload only
// carries a vault name, never a resource ID, and the vault may live in a
// different resource group, subscription or tenant than the cluster.
type KeyVaultKeysClientBuilder interface {
	// Build returns a KeyVaultKeysClient for the vault named vaultName, which
	// authenticates as the identity behind credential.
	Build(vaultName string, credential azcore.TokenCredential) (KeyVaultKeysClient, error)
}

// keyVaultKeysClientBuilder builds real azkeys clients against the Key Vault
// data plane endpoints of a single Azure cloud environment.
type keyVaultKeysClientBuilder struct {
	// keyVaultDNSSuffix is the cloud environment's Key Vault DNS suffix, for
	// example "vault.azure.net".
	keyVaultDNSSuffix string
	options           *azcore.ClientOptions
}

// NewKeyVaultKeysClientBuilder instantiates a KeyVaultKeysClientBuilder that
// builds clients against vaults in the cloud environment described by
// keyVaultDNSSuffix.
func NewKeyVaultKeysClientBuilder(keyVaultDNSSuffix string, options *azcore.ClientOptions) KeyVaultKeysClientBuilder {
	return &keyVaultKeysClientBuilder{
		keyVaultDNSSuffix: keyVaultDNSSuffix,
		options:           options,
	}
}

func (b *keyVaultKeysClientBuilder) Build(vaultName string, credential azcore.TokenCredential) (KeyVaultKeysClient, error) {
	if len(b.keyVaultDNSSuffix) == 0 {
		return nil, fmt.Errorf("key vault DNS suffix is not configured for this cloud environment")
	}

	var clientOptions *azkeys.ClientOptions
	if b.options != nil {
		clientOptions = &azkeys.ClientOptions{ClientOptions: *b.options}
	}

	// azkeys resolves the cloud-correct OAuth scope itself, through Key Vault's
	// challenge-based authentication, so no per-cloud scope is passed here.
	return azkeys.NewClient(fmt.Sprintf("https://%s.%s", vaultName, b.keyVaultDNSSuffix), credential, clientOptions)
}
