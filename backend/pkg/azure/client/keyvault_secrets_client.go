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
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// KeyVaultSecretsClient is an interface that defines the methods that we want
// to use from the azsecrets.Client type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/security/keyvault/azsecrets).
// The aim is to only contain methods that are defined on that SDK client.
// If you need to use a method provided by the Azure Go SDK client but it is
// not defined in this interface then it has to be added here and all the types
// implementing this interface have to implement the new method.
type KeyVaultSecretsClient interface {
	SetSecret(ctx context.Context, name string, parameters azsecrets.SetSecretParameters, options *azsecrets.SetSecretOptions) (azsecrets.SetSecretResponse, error)
	DeleteSecret(ctx context.Context, name string, options *azsecrets.DeleteSecretOptions) (azsecrets.DeleteSecretResponse, error)
	GetSecret(ctx context.Context, name string, version string, options *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error)
	RecoverDeletedSecret(ctx context.Context, name string, options *azsecrets.RecoverDeletedSecretOptions) (azsecrets.RecoverDeletedSecretResponse, error)
}

// interface guard to ensure that all methods defined in the KeyVaultSecretsClient
// interface are implemented by the real Azure Go SDK azsecrets.Client.
var _ KeyVaultSecretsClient = (*azsecrets.Client)(nil)
