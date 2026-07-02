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
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/internal/utils"
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

// ServiceManagedIdentityClientBuilder offers the ability tocreate Azure clients
// authenticating as the the Cluster's Service Managed Identity, which is
// a cluster-scoped identity.
type ServiceManagedIdentityClientBuilder interface {
	BuilderType() ServiceManagedIdentityClientBuilderType
	// UserAssignedIdentitiesClient returns a new User Assigned Identities client.
	UserAssignedIdentitiesClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (UserAssignedIdentitiesClient, error)
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
	// We obtain the Managed Identity Data Plane client using the Cluster's Identity URL.
	miDataplaneClient, err := b.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(clusterIdentityURL)
	if err != nil {
		return nil, err
	}

	// We then use the Managed Identity Data Plane client to get
	// credentials associated to the Cluster's Service Managed Identity.
	dataplaneRequest := dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{smiResourceID.String()},
	}
	resp, err := miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplaneRequest)
	if err != nil {
		return nil, err
	}
	if len(resp.ExplicitIdentities) == 0 {
		return nil,
			utils.TrackError(fmt.Errorf("managed identities data plane returned no credentials for the cluster's service managed identity '%s", smiResourceID.String()))
	}

	// We convert the received UserAssignedIdentityCredentials result into
	// an azidentity.ClientCertificateCredential, which Azure Go SDK's uses
	// to instantiate a UserAssignedIdentitiesClient.
	userAssignedIdentityCredential := resp.ExplicitIdentities[0]
	creds, err := dataplane.GetCredential(b.azCoreARMClientOptions.ClientOptions, userAssignedIdentityCredential)
	if err != nil {
		return nil, err
	}

	// We finally instantiate the UserAssignedIdentitiesClient using the
	// the credentials we obtained from the Managed Identities Data Plane Service.
	return armmsi.NewUserAssignedIdentitiesClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func NewServiceManagedIdentityClientBuilder(fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, options *azcorearm.ClientOptions) ServiceManagedIdentityClientBuilder {
	return &serviceManagedIdentityClientBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		azCoreARMClientOptions:      options,
	}
}
