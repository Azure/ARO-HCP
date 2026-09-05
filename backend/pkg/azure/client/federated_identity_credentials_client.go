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

//go:generate $MOCKGEN -typed -source=federated_identity_credentials_client.go -destination=mock_federated_identity_credentials_client.go -package client FederatedIdentityCredentialsClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

// FederatedIdentityCredentialsClient is an interface that defines the
// methods that we want to use from the FederatedIdentityCredentialsClient type in
// the Azure Go SDK (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/msi/armmsi).
// The aim is to only contain methods that are defined in the Azure Go SDK
// FederatedIdentityCredentialsClient client.
// If you need to use a method provided by the Azure Go SDK FederatedIdentityCredentialsClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type FederatedIdentityCredentialsClient interface {
	CreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, parameters armmsi.FederatedIdentityCredential, options *armmsi.FederatedIdentityCredentialsClientCreateOrUpdateOptions) (armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse, error)

	Delete(ctx context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, options *armmsi.FederatedIdentityCredentialsClientDeleteOptions) (armmsi.FederatedIdentityCredentialsClientDeleteResponse, error)

	Get(ctx context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, options *armmsi.FederatedIdentityCredentialsClientGetOptions) (armmsi.FederatedIdentityCredentialsClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the FederatedIdentityCredentialsClient
// interface are implemented by the real Azure Go SDK FederatedIdentityCredentialsClient
// client. This interface guard should always compile
var _ FederatedIdentityCredentialsClient = (*armmsi.FederatedIdentityCredentialsClient)(nil)
