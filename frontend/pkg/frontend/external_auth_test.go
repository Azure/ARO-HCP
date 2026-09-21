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

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestUpdateExternalAuthOperationWithoutInternalID(t *testing.T) {
	legacyID, err := metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/cluster/external_auth_config/external_auths/auth")
	require.NoError(t, err)

	for _, tc := range []struct {
		name             string
		clusterServiceID *metadataapi.InternalID
	}{
		{name: "without Cluster Service ID"},
		{name: "with legacy Cluster Service ID", clusterServiceID: &legacyID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			registry := coreapi.NewAPIRegistry()
			require.NoError(t, v20251223preview.RegisterVersion(registry))
			version, ok := registry.Lookup(string(metadataapi.APIVersionV20251223Preview))
			require.True(t, ok)
			ctx = ContextWithVersion(ctx, version)
			ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})

			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			f := &Frontend{resourcesDBClient: db, azureLocation: coreapitesting.TestLocation}
			externalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
			externalAuth.CosmosMetadata = coreapi.CosmosMetadata{
				ResourceID:   externalAuth.ID,
				PartitionKey: externalAuth.ID.SubscriptionID,
			}
			externalAuth.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
			externalAuth.ServiceProviderProperties.ClusterServiceID = tc.clusterServiceID
			client := db.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name)
			oldExternalAuth, err := client.Create(ctx, externalAuth, nil)
			require.NoError(t, err)

			updatedExternalAuth := oldExternalAuth.DeepCopy()
			updatedExternalAuth.Properties.Claim.Mappings.Username.Claim = "updated-claim"
			request := httptest.NewRequestWithContext(ctx, http.MethodPut, externalAuth.ID.String(), nil)
			response := httptest.NewRecorder()
			require.NoError(t, f.updateExternalAuthInCosmos(ctx, response, request, http.StatusOK, updatedExternalAuth, oldExternalAuth))
			require.Equal(t, http.StatusOK, response.Code)

			storedExternalAuth, err := client.Get(ctx, externalAuth.ID.Name)
			require.NoError(t, err)
			require.Equal(t, "updated-claim", storedExternalAuth.Properties.Claim.Mappings.Username.Claim)
			require.Equal(t, coreapi.ProvisioningStateAccepted, storedExternalAuth.Properties.ProvisioningState)
			require.Equal(t, tc.clusterServiceID, storedExternalAuth.ServiceProviderProperties.ClusterServiceID, "updating must preserve the resource's legacy Cluster Service ID")
			require.NotEmpty(t, storedExternalAuth.ServiceProviderProperties.ActiveOperationID)

			operation, err := db.Operations(externalAuth.ID.SubscriptionID).Get(ctx, storedExternalAuth.ServiceProviderProperties.ActiveOperationID)
			require.NoError(t, err)
			require.Equal(t, cosmosstorageutils.OperationRequestUpdate, operation.Request)
			require.Equal(t, externalAuth.ID.String(), operation.ExternalID.String())
			require.Empty(t, operation.InternalID.String(), "external auth operations must not depend on a Cluster Service ID")
		})
	}
}
