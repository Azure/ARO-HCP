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
)

// BackendIdentityTokenCredentialRetriever is a type that allows you to retrieve an azcore.TokenCredential
// associated to the backend identity.
type BackendIdentityTokenCredentialRetriever interface {
	RetrieveCredential() (azcore.TokenCredential, error)
}

type backendIdentityCredentialRetriever struct {
	options *azcore.ClientOptions
}

var _ BackendIdentityTokenCredentialRetriever = (*backendIdentityCredentialRetriever)(nil)

func (b *backendIdentityCredentialRetriever) RetrieveCredential() (azcore.TokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: *b.options,
		},
	)
}

func NewBackendIdentityTokenCredentialRetriever(options *azcore.ClientOptions) BackendIdentityTokenCredentialRetriever {
	return &backendIdentityCredentialRetriever{
		options: options,
	}
}
