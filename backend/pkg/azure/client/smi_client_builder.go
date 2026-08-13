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

//go:generate $MOCKGEN -typed -source=smi_client_builder.go -destination=mock_smi_client_builder.go -package client ServiceManagedIdentityClientBuilder

import (
	"context"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// ServiceManagedIdentityClientBuilderType is a type that represents the type of the
// ServiceManagedIdentityClientBuilder interface. It is used to ensure that
// that interface is incompatible with other client builder interfaces that
// might have the same set of methods
type ServiceManagedIdentityClientBuilderType string

const (
	// ServiceManagedIdentityClientBuilderTypeValue is the value of the ServiceManagedIdentityClientBuilderType type that
	// represents the SMI client builder.
	ServiceManagedIdentityClientBuilderTypeValue ServiceManagedIdentityClientBuilderType = "SMI"
)

// ServiceManagedIdentityClientBuilder offers the ability to create Azure clients
// authenticating as the Cluster's Service Managed Identity, which is
// a cluster-scoped identity.
type ServiceManagedIdentityClientBuilder interface {
	BuilderType() ServiceManagedIdentityClientBuilderType
	// UserAssignedIdentitiesClient returns a new User Assigned Identities client.
	UserAssignedIdentitiesClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (UserAssignedIdentitiesClient, error)
	// SubnetsClient returns a new Subnet client.
	SubnetsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (SubnetsClient, error)
}

type serviceManagedIdentityClientBuilder struct {
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder
	azCoreARMClientOptions      *azcorearm.ClientOptions
}

var _ ServiceManagedIdentityClientBuilder = (*serviceManagedIdentityClientBuilder)(nil)

func (b *serviceManagedIdentityClientBuilder) BuilderType() ServiceManagedIdentityClientBuilderType {
	return ServiceManagedIdentityClientBuilderTypeValue
}

func (b *serviceManagedIdentityClientBuilder) UserAssignedIdentitiesClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (UserAssignedIdentitiesClient, error) {
	creds, err := credentialForIdentity(ctx, b.fpaMIdataplaneClientBuilder, b.azCoreARMClientOptions.ClientOptions, clusterIdentityURL, smiResourceID)
	if err != nil {
		return nil, err
	}

	return armmsi.NewUserAssignedIdentitiesClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func (b *serviceManagedIdentityClientBuilder) SubnetsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (SubnetsClient, error) {
	creds, err := credentialForIdentity(ctx, b.fpaMIdataplaneClientBuilder, b.azCoreARMClientOptions.ClientOptions, clusterIdentityURL, smiResourceID)
	if err != nil {
		return nil, err
	}
	return armnetwork.NewSubnetsClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func NewServiceManagedIdentityClientBuilder(fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, options *azcorearm.ClientOptions) ServiceManagedIdentityClientBuilder {
	return &serviceManagedIdentityClientBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		azCoreARMClientOptions:      options,
	}
}
