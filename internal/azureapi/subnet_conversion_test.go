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

func TestClusterVNetIntegrationSubnetConversion(t *testing.T) {
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
	const subnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/swift"
	for _, versionName := range registry.ListVersions().UnsortedList() {
		t.Run(versionName, func(t *testing.T) {
			version, ok := registry.Lookup(versionName)
			require.True(t, ok)
			if versionName == string(metadataapi.APIVersionV20240610Preview) {
				cluster, err := version.NewCluster(nil).ConvertToInternal(nil)
				require.NoError(t, err, "old public create must remain convertible without a subnet")
				require.Nil(t, cluster.CustomerProperties.Platform.VnetIntegrationSubnetID)
				existing := cluster.DeepCopy()
				existing.CustomerProperties.Platform.VnetIntegrationSubnetID = metadataapi.Must(azcorearm.ParseResourceID(subnetID))
				cluster, err = version.NewCluster(existing).ConvertToInternal(existing)
				require.NoError(t, err)
				require.Equal(t, subnetID, cluster.CustomerProperties.Platform.VnetIntegrationSubnetID.String(), "old API updates preserve the unrepresentable subnet")
				return
			}
			for _, tt := range []struct {
				name    string
				body    string
				want    string
				wantErr string
			}{
				{name: "absent", body: `{"properties":{"platform":{}}}`},
				{name: "null", body: `{"properties":{"platform":{"vnetIntegrationSubnetId":null}}}`},
				{name: "empty", body: `{"properties":{"platform":{"vnetIntegrationSubnetId":""}}}`, wantErr: "field cannot be empty string"},
				{name: "malformed", body: `{"properties":{"platform":{"vnetIntegrationSubnetId":"invalid"}}}`, wantErr: "vnetIntegrationSubnetId"},
				{name: "valid", body: `{"properties":{"platform":{"vnetIntegrationSubnetId":"` + subnetID + `"}}}`, want: subnetID},
			} {
				for _, update := range []bool{false, true} {
					name := tt.name + "/create"
					if update {
						name = tt.name + "/legacy-update"
					}
					t.Run(name, func(t *testing.T) {
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
						require.NoError(t, err, "nil requiredness must be deferred to feature-aware validation")
						if tt.want == "" {
							require.Nil(t, cluster.CustomerProperties.Platform.VnetIntegrationSubnetID)
						} else {
							require.NotNil(t, cluster.CustomerProperties.Platform.VnetIntegrationSubnetID)
							require.Equal(t, tt.want, cluster.CustomerProperties.Platform.VnetIntegrationSubnetID.String())
						}
					})
				}
			}
		})
	}
}
