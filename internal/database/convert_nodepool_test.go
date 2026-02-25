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
	"testing"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestRoundTripNodePoolInternalCosmosInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *arm.Resource, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ID = api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/change-channel"))
			j.Name = "change-channel"
			j.Type = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
		},
		func(j *api.HCPOpenShiftClusterNodePool, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ServiceProviderProperties.ExistingCosmosUID = ""
			// Storage defaults are applied on Cosmos read, so ensure
			// defaulted fields are never zero during round-trip testing.
			if len(j.Properties.Platform.OSDisk.DiskStorageAccountType) == 0 {
				j.Properties.Platform.OSDisk.DiskStorageAccountType = api.DiskStorageAccountTypePremium_LRS
			}
		},
		func(j *arm.ManagedServiceIdentity, c randfill.Continue) {
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
		original := &api.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		roundTripInternalToCosmosToInternal[api.HCPOpenShiftClusterNodePool, NodePool](t, original)
	}
}

func TestApplyNodePoolStorageDefaults(t *testing.T) {
	tests := []struct {
		name                       string
		diskStorageAccountType     api.DiskStorageAccountType
		wantDiskStorageAccountType api.DiskStorageAccountType
	}{
		{
			name:                       "zero value gets default",
			diskStorageAccountType:     "",
			wantDiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
		},
		{
			name:                       "explicit Premium_LRS preserved",
			diskStorageAccountType:     api.DiskStorageAccountTypePremium_LRS,
			wantDiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
		},
		{
			name:                       "explicit StandardSSD_LRS preserved",
			diskStorageAccountType:     api.DiskStorageAccountTypeStandardSSD_LRS,
			wantDiskStorageAccountType: api.DiskStorageAccountTypeStandardSSD_LRS,
		},
		{
			name:                       "explicit Standard_LRS preserved",
			diskStorageAccountType:     api.DiskStorageAccountTypeStandard_LRS,
			wantDiskStorageAccountType: api.DiskStorageAccountTypeStandard_LRS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			np := &api.HCPOpenShiftClusterNodePool{}
			np.Properties.Platform.OSDisk.DiskStorageAccountType = tt.diskStorageAccountType

			applyNodePoolStorageDefaults(np)

			if np.Properties.Platform.OSDisk.DiskStorageAccountType != tt.wantDiskStorageAccountType {
				t.Errorf("DiskStorageAccountType = %q, want %q",
					np.Properties.Platform.OSDisk.DiskStorageAccountType,
					tt.wantDiskStorageAccountType)
			}
		})
	}
}
