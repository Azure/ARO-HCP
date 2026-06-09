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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// MIDataplaneBasedIdentityAccessTokenRetriever retrieves Azure access tokens for an
// identity managed through the Managed Identities Data Plane (MI Dataplane) service.
//
// GetToken uses the same signature as azcore.TokenCredential, so production
// implementations can also satisfy that interface for Azure SDK clients that need it.
//
// Each call to GetToken fetches fresh credentials for the bound identity from the
// MI Dataplane service and exchanges them for an access token.
//
// Note: when the service is configured with a hardcoded Managed Identities Data Plane client
// (e.g. in local development), GetToken always returns a token for the Azure Managed Identity Mock
// Identity regardless of which real identity it was built for.
type MIDataplaneBasedIdentityAccessTokenRetriever interface {
	GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)
}

// MIDataplaneBasedIdentityAccessTokenRetrieverBuilder creates
// MIDataplaneBasedIdentityAccessTokenRetriever instances for specific cluster-scoped
// identities. Because MI Dataplane-based identities are cluster-scoped, they can only
// be fully resolved when a cluster is being processed; this builder defers that
// resolution to the point of use, keeping callers decoupled from identity lifecycle.
//
// Note: when the service is configured with a hardcoded Managed Identities Data Plane client,
// Build returns a retriever that always issues tokens for the MI Mock Identity,
// ignoring the clusterIdentityURL and identityResourceID arguments passed to Build.
type MIDataplaneBasedIdentityAccessTokenRetrieverBuilder interface {
	// Build constructs an MIDataplaneBasedIdentityAccessTokenRetriever for the given
	// identity. The identity is specified by the cluster's Managed Identity Data Plane endpoint
	// URL (clusterIdentityURL) and the identity's Azure resource ID (identityResourceID).
	//
	// Build resolves the cluster-scoped MI Dataplane client for clusterIdentityURL.
	// Fetching credentials and exchanging them for tokens is deferred to each
	// subsequent GetToken call on the returned retriever.
	Build(clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (MIDataplaneBasedIdentityAccessTokenRetriever, error)
}

// miDataplaneBasedIdentityAccessTokenRetriever performs a full MI Dataplane round-trip on every GetToken call to acquire the freshest possible credentials.
type miDataplaneBasedIdentityAccessTokenRetriever struct {
	miDataplaneClient  ManagedIdentitiesDataplaneClient
	clientOptions      *azcore.ClientOptions
	identityResourceID *azcorearm.ResourceID
}

// Compile-time guards: the concrete retriever satisfies our interface and also azcore.TokenCredential via its GetToken method.
var _ MIDataplaneBasedIdentityAccessTokenRetriever = (*miDataplaneBasedIdentityAccessTokenRetriever)(nil)
var _ azcore.TokenCredential = (*miDataplaneBasedIdentityAccessTokenRetriever)(nil)

// GetToken acquires an access token for the bound identity by:
//  1. Fetching credentials for the identity via GetUserAssignedIdentitiesCredentials.
//  2. Building an azcore.TokenCredential from the returned credentials.
//  3. Calling GetToken on that credential with the provided token request options.
func (r *miDataplaneBasedIdentityAccessTokenRetriever) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	resp, err := r.miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{r.identityResourceID.String()},
	})
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(fmt.Errorf("failed to get managed identity credentials: %w", err))
	}
	if len(resp.ExplicitIdentities) == 0 {
		return azcore.AccessToken{},
			utils.TrackError(fmt.Errorf("managed identities data plane returned no credentials for managed identity '%s'", r.identityResourceID.String()))
	}

	creds, err := dataplane.GetCredential(*r.clientOptions, resp.ExplicitIdentities[0])
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(fmt.Errorf("failed to build token credential for managed identity '%s': %w", r.identityResourceID.String(), err))
	}

	return creds.GetToken(ctx, options)
}

// miDataplaneBasedIdentityAccessTokenRetrieverBuilder is the production implementation of MIDataplaneBasedIdentityAccessTokenRetrieverBuilder.
type miDataplaneBasedIdentityAccessTokenRetrieverBuilder struct {
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder
	clientOptions               *azcore.ClientOptions
}

var _ MIDataplaneBasedIdentityAccessTokenRetrieverBuilder = (*miDataplaneBasedIdentityAccessTokenRetrieverBuilder)(nil)

// Build creates a new MIDataplaneBasedIdentityAccessTokenRetriever bound to the given identity. It resolves the cluster-scoped MI Dataplane client for clusterIdentityURL;
// credential fetch and token exchange are deferred to each GetToken call.
func (b *miDataplaneBasedIdentityAccessTokenRetrieverBuilder) Build(clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (MIDataplaneBasedIdentityAccessTokenRetriever, error) {
	miDataplaneClient, err := b.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(clusterIdentityURL)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get managed identity dataplane client: %w", err))
	}
	return &miDataplaneBasedIdentityAccessTokenRetriever{
		miDataplaneClient:  miDataplaneClient,
		clientOptions:      b.clientOptions,
		identityResourceID: identityResourceID,
	}, nil
}

// NewMIDataplaneBasedIdentityAccessTokenRetrieverBuilder creates a builder that produces MIDataplaneBasedIdentityAccessTokenRetriever instances using the supplied MI Dataplane client builder and Azure core client options.
func NewMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, clientOptions azcore.ClientOptions) MIDataplaneBasedIdentityAccessTokenRetrieverBuilder {
	return &miDataplaneBasedIdentityAccessTokenRetrieverBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		clientOptions:               &clientOptions,
	}
}
