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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// BackendIdentityClientBuilderType is a type that represents the type of the BackendIdentityClientBuilder
// interface. It is used to ensure that that interface is incompatible
// with other client builder interfaces that might have the same set of
// methods.
type BackendIdentityClientBuilderType string

const (
	// BackendIdentityClientBuilderTypeValue is the value of the BackendIdentityClientBuilderType type that
	// represents the backend identity client builder.
	BackendIdentityClientBuilderTypeValue BackendIdentityClientBuilderType = "BackendIdentity"
)

// BackendIdentityClientBuilder builds Azure clients that interact with the Azure
// platform as the backend identity. The backend identity is used to interact
// with Red Hat side Azure infrastructure.
type BackendIdentityClientBuilder interface {
	// BuilderType returns the type of the client builder. Its only
	// purpose is to ensure that this interface is incompatible
	// with other client builder interfaces that might have the same
	// set of methods. In that way we ensure that they cannot be used
	// interchangeably.
	BuilderType() BackendIdentityClientBuilderType
	DataplaneIdentitiesOIDCConfigurationBlobStorageClient() (BlobStorageClient, error)
	RoleDefinitionsClient() (RoleDefinitionsClient, error)
	KeyVaultSecretClient(vaultURL string) (KeyVaultSecretClient, error)
}

type backendIdentityClientBuilder struct {
	credential       azcore.TokenCredential
	azCoreClientOpts *azcore.ClientOptions
	armClientOpts    *azcorearm.ClientOptions
	blobServiceURL   string
}

var _ BackendIdentityClientBuilder = (*backendIdentityClientBuilder)(nil)

// NewBackendIdentityClientBuilder instantiates a BackendIdentityClientBuilder. When clients
// are instantiated with it, the provided credential and client options are used.
func NewBackendIdentityClientBuilder(
	credential azcore.TokenCredential,
	azCoreClientOpts *azcore.ClientOptions,
	armClientOpts *azcorearm.ClientOptions,
	blobServiceURL string,
) BackendIdentityClientBuilder {
	return &backendIdentityClientBuilder{
		credential:       credential,
		azCoreClientOpts: azCoreClientOpts,
		armClientOpts:    armClientOpts,
		blobServiceURL:   blobServiceURL,
	}
}

func (b *backendIdentityClientBuilder) BuilderType() BackendIdentityClientBuilderType {
	return BackendIdentityClientBuilderTypeValue
}

func (b *backendIdentityClientBuilder) DataplaneIdentitiesOIDCConfigurationBlobStorageClient() (BlobStorageClient, error) {
	return azblob.NewClient(
		b.blobServiceURL,
		b.credential,
		&azblob.ClientOptions{
			ClientOptions: *b.azCoreClientOpts,
		},
	)
}

func (b *backendIdentityClientBuilder) RoleDefinitionsClient() (RoleDefinitionsClient, error) {
	return armauthorization.NewRoleDefinitionsClient(b.credential, b.armClientOpts)
}

func (b *backendIdentityClientBuilder) KeyVaultSecretClient(vaultURL string) (KeyVaultSecretClient, error) {
	return azsecrets.NewClient(
		vaultURL,
		b.credential,
		&azsecrets.ClientOptions{
			ClientOptions: *b.azCoreClientOpts,
		},
	)
}
