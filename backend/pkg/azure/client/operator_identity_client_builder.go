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

//go:generate $MOCKGEN -typed -source=operator_identity_client_builder.go -destination=mock_operator_identity_client_builder.go -package client ClusterOperatorIdentityClientBuilder

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// ClusterOperatorIdentityClientBuilderType is a type that represents the type of the
// ClusterOperatorIdentityClientBuilder interface. It is used to ensure that
// that interface is incompatible with other client builder interfaces that
// might have the same set of methods.
type ClusterOperatorIdentityClientBuilderType string

const (
	// ClusterOperatorIdentityClientBuilderTypeValue is the value of the
	// ClusterOperatorIdentityClientBuilderType type that represents the
	// cluster operator identity client builder.
	ClusterOperatorIdentityClientBuilderTypeValue ClusterOperatorIdentityClientBuilderType = "ClusterOperatorIdentity"
)

// ClusterOperatorIdentityClientBuilder offers the ability to create Azure clients
// authenticating as a cluster's operator identity (e.g. "kms", "cloud-controller",
// etc.). These are customer-granted managed identities that operators use to
// access customer resources.
type ClusterOperatorIdentityClientBuilder interface {
	BuilderType() ClusterOperatorIdentityClientBuilderType
	// KeyVaultKeysClient returns a new Key Vault Keys data plane client,
	// authenticated as the given cluster operator identity. This is the
	// credential path that has RBAC access to a customer-owned Key Vault;
	// the FPA and SMI do not have such access.
	KeyVaultKeysClient(ctx context.Context, clusterIdentityURL string, operatorIdentityResourceID *azcorearm.ResourceID, vaultName string) (KeyVaultKeysClient, error)
}

type clusterOperatorIdentityClientBuilder struct {
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder
	azCoreARMClientOptions      *azcorearm.ClientOptions
}

var _ ClusterOperatorIdentityClientBuilder = (*clusterOperatorIdentityClientBuilder)(nil)

func (b *clusterOperatorIdentityClientBuilder) BuilderType() ClusterOperatorIdentityClientBuilderType {
	return ClusterOperatorIdentityClientBuilderTypeValue
}

func (b *clusterOperatorIdentityClientBuilder) KeyVaultKeysClient(ctx context.Context, clusterIdentityURL string, operatorIdentityResourceID *azcorearm.ResourceID, vaultName string) (KeyVaultKeysClient, error) {
	creds, err := credentialForIdentity(ctx, b.fpaMIdataplaneClientBuilder, b.azCoreARMClientOptions.ClientOptions, clusterIdentityURL, operatorIdentityResourceID)
	if err != nil {
		return nil, err
	}

	vaultURL := fmt.Sprintf("https://%s.vault.azure.net/", vaultName)
	return azkeys.NewClient(vaultURL, creds, nil)
}

func NewClusterOperatorIdentityClientBuilder(fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, options *azcorearm.ClientOptions) ClusterOperatorIdentityClientBuilder {
	return &clusterOperatorIdentityClientBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		azCoreARMClientOptions:      options,
	}
}
