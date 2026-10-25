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
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// ErrSMIClientBuilderIdentityNotFound indicates that serviceManagedIdentityClientBuilder
// failed to build a client because the cluster's Service Managed Identity no longer
// exists in Azure. The Managed Identities Data Plane API does not return a
// distinguishable error for this case; it returns the required credential fields
// (ClientID, ClientSecret, TenantID, AuthenticationEndpoint) as nil instead. This is
// deliberately kept local to this file rather than in errors.go: unlike the
// azcore.ResponseError-based predicates there (which can apply to any ARM call), this
// error is specific to serviceManagedIdentityClientBuilder and never comes from a raw
// Azure API response.
var ErrSMIClientBuilderIdentityNotFound = errors.New("service managed identity does not exist in Azure")

// IsSMIClientBuilderIdentityNotFoundErr reports whether err means
// serviceManagedIdentityClientBuilder failed to build a client because the cluster's
// Service Managed Identity no longer exists in Azure. It only ever fires for errors
// returned by serviceManagedIdentityClientBuilder's own methods; it is not a
// general-purpose Azure "not found" check like the predicates in errors.go.
func IsSMIClientBuilderIdentityNotFoundErr(err error) bool {
	return errors.Is(err, ErrSMIClientBuilderIdentityNotFound)
}

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
	// NetworkSecurityGroupsClient returns a new Network Security Groups client
	// authenticated as the cluster's Service Managed Identity.
	NetworkSecurityGroupsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (NetworkSecurityGroupsClient, error)
	// SubnetsClient returns a new Subnets client authenticated as the cluster's
	// Service Managed Identity.
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

// credentialsForServiceManagedIdentity acquires an azcore.TokenCredential for the cluster's
// Service Managed Identity by going through the Managed Identities Data Plane.
func (b *serviceManagedIdentityClientBuilder) credentialsForServiceManagedIdentity(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID) (azcore.TokenCredential, error) {
	miDataplaneClient, err := b.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(clusterIdentityURL)
	if err != nil {
		return nil, err
	}

	dataplaneRequest := dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{smiResourceID.String()},
	}
	resp, err := miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplaneRequest)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get user assigned identities credentials: %w", err))
	}
	if len(resp.ExplicitIdentities) == 0 {
		return nil, utils.TrackError(fmt.Errorf("managed identities data plane returned no credentials for the cluster's service managed identity '%s'", smiResourceID.String()))
	}

	identity := resp.ExplicitIdentities[0]

	if miDataplaneIdentityCredentialFieldsMissing(identity) {
		return nil, utils.TrackError(fmt.Errorf("%w: %q", ErrSMIClientBuilderIdentityNotFound, smiResourceID.String()))
	}

	return dataplane.GetCredential(b.azCoreARMClientOptions.ClientOptions, identity)
}

func (b *serviceManagedIdentityClientBuilder) UserAssignedIdentitiesClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (UserAssignedIdentitiesClient, error) {
	creds, err := b.credentialsForServiceManagedIdentity(ctx, clusterIdentityURL, smiResourceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get credentials for service managed identity: %w", err))
	}
	return armmsi.NewUserAssignedIdentitiesClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func (b *serviceManagedIdentityClientBuilder) SubnetsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (SubnetsClient, error) {
	creds, err := b.credentialsForServiceManagedIdentity(ctx, clusterIdentityURL, smiResourceID)
	if err != nil {
		return nil, err
	}
	return armnetwork.NewSubnetsClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func (b *serviceManagedIdentityClientBuilder) NetworkSecurityGroupsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (NetworkSecurityGroupsClient, error) {
	creds, err := b.credentialsForServiceManagedIdentity(ctx, clusterIdentityURL, smiResourceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get credentials for service managed identity: %w", err))
	}

	// We instantiate the SecurityGroupsClient using the
	// credentials we obtained from the Managed Identities Data Plane Service.
	return armnetwork.NewSecurityGroupsClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func NewServiceManagedIdentityClientBuilder(fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, options *azcorearm.ClientOptions) ServiceManagedIdentityClientBuilder {
	return &serviceManagedIdentityClientBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		azCoreARMClientOptions:      options,
	}
}
