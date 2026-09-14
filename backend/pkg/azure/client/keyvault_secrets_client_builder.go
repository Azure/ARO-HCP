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

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// KeyVaultSecretsClientBuilderType is a type that represents the type of the
// KeyVaultSecretsClientBuilder interface. It is used to ensure that the
// interface is incompatible with other client builder interfaces that might
// have the same set of methods.
type KeyVaultSecretsClientBuilderType string

const (
	// KeyVaultSecretsClientBuilderTypeValue is the value of the
	// KeyVaultSecretsClientBuilderType type that represents the backend
	// identity Key Vault secrets client builder.
	KeyVaultSecretsClientBuilderTypeValue KeyVaultSecretsClientBuilderType = "BackendIdentity-KeyVaultSecrets"
)

// KeyVaultSecretsClientBuilder offers the ability to create Azure Key Vault
// secrets clients authenticating as the backend identity. The backend identity
// is used to interact with Red Hat side Azure infrastructure, including the
// hosted-clusters managed identities Key Vault on each management cluster.
type KeyVaultSecretsClientBuilder interface {
	BuilderType() KeyVaultSecretsClientBuilderType
	// SecretsClient returns a new Key Vault secrets client for the given vault URL.
	SecretsClient(vaultURL string) (KeyVaultSecretsClient, error)
}

type backendIdentityKeyVaultSecretsClientBuilder struct {
	credential    azcore.TokenCredential
	clientOptions *azcore.ClientOptions
}

var _ KeyVaultSecretsClientBuilder = (*backendIdentityKeyVaultSecretsClientBuilder)(nil)

func (b *backendIdentityKeyVaultSecretsClientBuilder) BuilderType() KeyVaultSecretsClientBuilderType {
	return KeyVaultSecretsClientBuilderTypeValue
}

func (b *backendIdentityKeyVaultSecretsClientBuilder) SecretsClient(vaultURL string) (KeyVaultSecretsClient, error) {
	if len(vaultURL) == 0 {
		return nil, utils.TrackError(fmt.Errorf("key vault URL is empty"))
	}
	client, err := azsecrets.NewClient(vaultURL, b.credential, &azsecrets.ClientOptions{
		ClientOptions: *b.clientOptions,
	})
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create key vault secrets client for %s: %w", vaultURL, err))
	}
	return client, nil
}

// NewBackendIdentityKeyVaultSecretsClientBuilder provides a new instance of
// KeyVaultSecretsClientBuilder that creates Key Vault secrets clients
// authenticating as the backend identity.
func NewBackendIdentityKeyVaultSecretsClientBuilder(credential azcore.TokenCredential, clientOptions *azcore.ClientOptions) KeyVaultSecretsClientBuilder {
	return &backendIdentityKeyVaultSecretsClientBuilder{
		credential:    credential,
		clientOptions: clientOptions,
	}
}
