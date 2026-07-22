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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

type managedIdentityKeyVaultSecretClientFactory struct {
	clientOptions azcore.ClientOptions
}

var _ KeyVaultSecretClientFactory = (*managedIdentityKeyVaultSecretClientFactory)(nil)

func NewManagedIdentityKeyVaultSecretClientFactory(clientOptions azcore.ClientOptions) KeyVaultSecretClientFactory {
	return &managedIdentityKeyVaultSecretClientFactory{
		clientOptions: clientOptions,
	}
}

func (f *managedIdentityKeyVaultSecretClientFactory) KeyVaultSecretClient(managedIdentityClientID string, vaultURL string) (KeyVaultSecretClient, error) {
	cred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
		ID:            azidentity.ClientID(managedIdentityClientID),
		ClientOptions: f.clientOptions,
	})
	if err != nil {
		return nil, err
	}
	return azsecrets.NewClient(
		vaultURL,
		cred,
		&azsecrets.ClientOptions{
			ClientOptions: f.clientOptions,
		},
	)
}
