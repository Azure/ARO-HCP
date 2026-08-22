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
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// credentialForIdentity obtains an azcore.TokenCredential for the given
// customer-granted managed identity, via the Managed Identities Data Plane
// Service, using the cluster's identity URL. It works for any identity the
// cluster has been granted (the SMI, or a cluster operator identity like
// "kms") -- the data plane request is keyed by resource ID alone.
func credentialForIdentity(
	ctx context.Context,
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder,
	azCoreClientOptions azcore.ClientOptions,
	clusterIdentityURL string,
	identityResourceID *azcorearm.ResourceID,
) (azcore.TokenCredential, error) {
	miDataplaneClient, err := fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(clusterIdentityURL)
	if err != nil {
		return nil, err
	}

	dataplaneRequest := dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{identityResourceID.String()},
	}
	resp, err := miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplaneRequest)
	if err != nil {
		return nil, err
	}
	if len(resp.ExplicitIdentities) == 0 {
		return nil,
			utils.TrackError(fmt.Errorf("managed identities data plane returned no credentials for identity '%s'", identityResourceID.String()))
	}

	return dataplane.GetCredential(azCoreClientOptions, resp.ExplicitIdentities[0])
}
