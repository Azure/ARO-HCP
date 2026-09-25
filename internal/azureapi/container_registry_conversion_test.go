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

package azureapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001preview"
)

func TestClusterContainerRegistryPreservation(t *testing.T) {
	registry := coreapi.NewAPIRegistry()
	for _, register := range []func(coreapi.APIRegistry) error{
		v20240610preview.RegisterVersion,
		v20251223preview.RegisterVersion,
		v20260630preview.RegisterVersion,
		v20260901preview.RegisterVersion,
		v20261001preview.RegisterVersion,
	} {
		require.NoError(t, register(registry))
	}

	miResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-mi"))

	for _, versionName := range registry.ListVersions().UnsortedList() {
		t.Run(versionName, func(t *testing.T) {
			version, ok := registry.Lookup(versionName)
			require.True(t, ok)

			// ContainerRegistry only exists in v20261001preview and newer
			if versionName != string(metadataapi.APIVersionV20261001Preview) {
				// For older versions, test that the field is preserved on update
				// even though the version can't represent it in the external API
				existing, err := version.NewCluster(nil).ConvertToInternal(nil)
				require.NoError(t, err)

				// Set ContainerRegistry on the existing cluster (from a newer version)
				existing.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity = miResourceID

				// Create an update request via this older API version that doesn't mention containerRegistry
				external := version.NewCluster(existing)
				body := `{"properties":{"platform":{"operatorsAuthentication":{"userAssignedIdentities":{}}}}}`
				require.NoError(t, json.Unmarshal([]byte(body), external))

				// Convert to internal with existing cluster
				cluster, err := external.ConvertToInternal(existing)
				require.NoError(t, err)

				// Assert containerRegistry is preserved (not lost in the update)
				require.NotNil(t, cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity)
				require.Equal(t, miResourceID.String(),
					cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity.String(),
					"older API versions must preserve containerRegistry on update")
				return
			}

			// For v20261001preview, test that containerRegistry can be set/updated
			for _, tt := range []struct {
				name    string
				body    string
				want    *azcorearm.ResourceID
				wantErr string
			}{
				{name: "absent", body: `{"properties":{"platform":{}}}`},
				{name: "null", body: `{"properties":{"platform":{"containerRegistry":null}}}`},
				{name: "valid", body: `{"properties":{"platform":{"containerRegistry":{"managedIdentity":"` + miResourceID.String() + `"}}}}`, want: miResourceID},
			} {
				for _, update := range []bool{false, true} {
					testName := tt.name + "/create"
					if update {
						testName = tt.name + "/update"
					}
					t.Run(testName, func(t *testing.T) {
						var existing *coreapi.Cluster
						if update {
							var err error
							existing, err = version.NewCluster(nil).ConvertToInternal(nil)
							require.NoError(t, err)
						}
						external := version.NewCluster(existing)
						require.NoError(t, json.Unmarshal([]byte(tt.body), external))
						cluster, err := external.ConvertToInternal(existing)
						if tt.wantErr != "" {
							require.ErrorContains(t, err, tt.wantErr)
							return
						}
						require.NoError(t, err)
						if tt.want == nil {
							require.Nil(t, cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity)
						} else {
							require.NotNil(t, cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity)
							require.Equal(t, tt.want.String(),
								cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity.String())
						}
					})
				}
			}
		})
	}
}
