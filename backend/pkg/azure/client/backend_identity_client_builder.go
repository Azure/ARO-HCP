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
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// BackendIdentityClientBuilderType is a type that represents the type of
// the BackendIdentityClientBuilder interface. It is used to ensure that
// that interface is incompatible with other client builder interfaces that
// might have the same set of methods
type BackendIdentityClientBuilderType string

const (
	// BackendIdentityClientBuilderTypeValue is the value of the
	// BackendIdentityClientBuilderType type that represents the Backend Identity
	// client builder.
	BackendIdentityClientBuilderTypeValue BackendIdentityClientBuilderType = "BACKEND_IDENTITY"
)

// BackendIdentityClientBuilder is a type that allows you to build Azure clients
// using the azure identity of the backend service. In K8s based deployments
// this is the azure identity associated to the backend K8s Deployment.
type BackendIdentityClientBuilder interface {
	BuilderType() BackendIdentityClientBuilderType
	BlobStorageClient(serviceURL string) (BlobStorageClient, error)
}

type backendIdentityClientBuilder struct {
	backendIdentityTokenCredRetriever BackendIdentityTokenCredentialRetriever
	options                           *azcore.ClientOptions
}

var _ BackendIdentityClientBuilder = (*backendIdentityClientBuilder)(nil)

func (b *backendIdentityClientBuilder) BuilderType() BackendIdentityClientBuilderType {
	return BackendIdentityClientBuilderTypeValue
}

func (b *backendIdentityClientBuilder) BlobStorageClient(serviceURL string) (BlobStorageClient, error) {
	creds, err := b.backendIdentityTokenCredRetriever.RetrieveCredential()
	if err != nil {
		return nil, err
	}

	return azblob.NewClient(
		serviceURL,
		creds,
		&azblob.ClientOptions{
			ClientOptions: *b.options,
		},
	)
}

func NewBackendIdentityClientBuilder(
	backendIdentityTokenCredRetriever BackendIdentityTokenCredentialRetriever, options *azcore.ClientOptions,
) BackendIdentityClientBuilder {
	return &backendIdentityClientBuilder{
		backendIdentityTokenCredRetriever: backendIdentityTokenCredRetriever,
		options:                           options,
	}
}
