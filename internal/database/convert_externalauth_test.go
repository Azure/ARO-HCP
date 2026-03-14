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

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/randfill"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestRoundTripExternalAuthInternalCosmosInternal(t *testing.T) {
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
		func(j *api.HCPOpenShiftClusterExternalAuth, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ServiceProviderProperties.ExistingCosmosUID = ""
			j.CosmosETag = ""
			// Canonical defaults are applied on Cosmos read, so ensure
			// defaulted fields are never zero during round-trip testing.
			if len(j.Properties.Claim.Mappings.Username.PrefixPolicy) == 0 {
				j.Properties.Claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyNone
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
		original := &api.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		roundTripInternalToCosmosToInternal[api.HCPOpenShiftClusterExternalAuth, ExternalAuth](t, original)
	}
}

func TestCosmosToInternalExternalAuthPreservesETag(t *testing.T) {
	expectedETag := azcore.ETag("test-etag-value-12345")
	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster"))

	cosmosObj := &ExternalAuth{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				CosmosETag: expectedETag,
			},
		},
		ExternalAuthProperties: ExternalAuthProperties{
			ResourceDocument: &ResourceDocument{
				ResourceID: resourceID,
			},
		},
	}

	internalObj, err := CosmosToInternalExternalAuth(cosmosObj)
	require.NoError(t, err)
	require.Equal(t, expectedETag, internalObj.GetCosmosData().CosmosETag)
}
