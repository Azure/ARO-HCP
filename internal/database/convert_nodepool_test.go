// Copyright 2025 Microsoft Corporation
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

package database

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/randfill"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

func TestRoundTripNodePoolInternalCosmosInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *resourcesapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *armresourcesapi.Resource, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ID = resourcesapi.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/change-channel"))
			j.Name = "change-channel"
			j.Type = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
		},
		func(j *resourcesapi.HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			// Match CosmosToInternalNodePool: empty InternalID becomes nil on read. Round-trip
			// must always carry a non-empty node pool ClusterServiceID (OCM node pool href).
			clusterID := "r" + strings.ReplaceAll(c.String(10), "/", "-")
			nodePoolID := strings.ReplaceAll(c.String(10), "/", "-")
			foo := resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + clusterID + "/node_pools/" + nodePoolID))
			j.ClusterServiceID = &foo
		},
		func(j *resourcesapi.HCPOpenShiftClusterNodePool, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ServiceProviderProperties.ExistingCosmosUID = ""
			j.CosmosETag = ""
			// Canonical defaults are applied on Cosmos read, so ensure
			// defaulted fields are never zero during round-trip testing.
			if len(j.Properties.Platform.OSDisk.DiskStorageAccountType) == 0 {
				j.Properties.Platform.OSDisk.DiskStorageAccountType = resourcesapi.DiskStorageAccountTypePremium_LRS
			}
			if len(j.Properties.Platform.OSDisk.DiskType) == 0 {
				j.Properties.Platform.OSDisk.DiskType = resourcesapi.OsDiskTypeManaged
			}
		},
		func(j *armresourcesapi.ManagedServiceIdentity, c randfill.Continue) {
			c.FillNoCustom(j)

			// we only round trip keys, so only fill in keys
			if j != nil && j.UserAssignedIdentities != nil {
				for k := range j.UserAssignedIdentities {
					j.UserAssignedIdentities[k] = nil
				}
			}
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &resourcesapi.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		roundTripInternalToCosmosToInternal[resourcesapi.HCPOpenShiftClusterNodePool, NodePool](t, original)
	}
}

func TestCosmosToInternalNodePoolPreservesETag(t *testing.T) {
	expectedETag := azcore.ETag("test-etag-value-12345")
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster/nodePools/my-np"))

	cosmosObj := &NodePool{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				CosmosETag: expectedETag,
			},
		},
		NodePoolProperties: NodePoolProperties{
			IntermediateResourceDoc: &ResourceDocument{
				ResourceID: resourceID,
			},
		},
	}

	internalObj, err := CosmosToInternalNodePool(cosmosObj)
	require.NoError(t, err)
	require.Equal(t, expectedETag, internalObj.GetCosmosData().CosmosETag)
}

func TestNodePoolEnsureDefaults(t *testing.T) {
	tests := []struct {
		name                       string
		diskStorageAccountType     resourcesapi.DiskStorageAccountType
		wantDiskStorageAccountType resourcesapi.DiskStorageAccountType
		diskType                   resourcesapi.OsDiskType
		wantDiskType               resourcesapi.OsDiskType
	}{
		{
			name:                       "zero values get defaults",
			diskStorageAccountType:     "",
			wantDiskStorageAccountType: resourcesapi.DiskStorageAccountTypePremium_LRS,
			diskType:                   "",
			wantDiskType:               resourcesapi.OsDiskTypeManaged,
		},
		{
			name:                       "explicit Premium_LRS preserved",
			diskStorageAccountType:     resourcesapi.DiskStorageAccountTypePremium_LRS,
			wantDiskStorageAccountType: resourcesapi.DiskStorageAccountTypePremium_LRS,
			diskType:                   resourcesapi.OsDiskTypeManaged,
			wantDiskType:               resourcesapi.OsDiskTypeManaged,
		},
		{
			name:                       "explicit StandardSSD_LRS preserved",
			diskStorageAccountType:     resourcesapi.DiskStorageAccountTypeStandardSSD_LRS,
			wantDiskStorageAccountType: resourcesapi.DiskStorageAccountTypeStandardSSD_LRS,
			diskType:                   resourcesapi.OsDiskTypeEphemeral,
			wantDiskType:               resourcesapi.OsDiskTypeEphemeral,
		},
		{
			name:                       "explicit Standard_LRS preserved",
			diskStorageAccountType:     resourcesapi.DiskStorageAccountTypeStandard_LRS,
			wantDiskStorageAccountType: resourcesapi.DiskStorageAccountTypeStandard_LRS,
			diskType:                   resourcesapi.OsDiskTypeManaged,
			wantDiskType:               resourcesapi.OsDiskTypeManaged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			np := &resourcesapi.HCPOpenShiftClusterNodePool{}
			np.Properties.Platform.OSDisk.DiskStorageAccountType = tt.diskStorageAccountType
			np.Properties.Platform.OSDisk.DiskType = tt.diskType

			np.EnsureDefaults()

			if np.Properties.Platform.OSDisk.DiskStorageAccountType != tt.wantDiskStorageAccountType {
				t.Errorf("DiskStorageAccountType = %q, want %q",
					np.Properties.Platform.OSDisk.DiskStorageAccountType,
					tt.wantDiskStorageAccountType)
			}
			if np.Properties.Platform.OSDisk.DiskType != tt.wantDiskType {
				t.Errorf("DiskType = %q, want %q",
					np.Properties.Platform.OSDisk.DiskType,
					tt.wantDiskType)
			}
		})
	}
}
