package database

import (
	"math/rand"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
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
			j.ID = "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/change-channel"
			j.Name = "change-channel"
			j.Type = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
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
