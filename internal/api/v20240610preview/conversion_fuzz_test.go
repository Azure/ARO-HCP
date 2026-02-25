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

package v20240610preview

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestRoundTripInternalExternalInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *arm.CosmosMetadata, c randfill.Continue) {
			c.Fill(j)

			if j != nil {
				j.CosmosETag = ""
			}
		},
		func(j *api.HCPOpenShiftCluster, c randfill.Continue) {
			c.Fill(j)
			j.ID = j.ResourceID
		},
		func(j *api.HCPOpenShiftClusterNodePool, c randfill.Continue) {
			c.Fill(j)
			j.ID = j.ResourceID
		},
		func(j *api.HCPOpenShiftClusterExternalAuth, c randfill.Continue) {
			c.Fill(j)
			j.ID = j.ResourceID
		},
		func(j *api.HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ActiveOperationID does not roundtrip through the external type because it is purely an internal detail
			j.ActiveOperationID = ""
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ocm.InternalID{}
			j.ExistingCosmosUID = ""
			// ExperimentalFeatures does not roundtrip through the external type because it is purely an internal detail
			j.ExperimentalFeatures = api.ExperimentalFeatures{}
		},
		func(j *api.HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ActiveOperationID does not roundtrip through the external type because it is purely an internal detail
			j.ActiveOperationID = ""
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ocm.InternalID{}
			j.ExistingCosmosUID = ""
		},
		func(j *api.HCPOpenShiftClusterExternalAuthServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ActiveOperationID does not roundtrip through the external type because it is purely an internal detail
			j.ActiveOperationID = ""
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ocm.InternalID{}
			j.ExistingCosmosUID = ""
		},
		func(j *api.CustomerManagedEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			// we cannot properly roundtrip a zero value here, so nil when that happens
			zeroValueKMS := api.KmsEncryptionProfile{}
			if j.Kms != nil && *j.Kms == zeroValueKMS {
				j.Kms = nil
			}
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		roundTripHCPCluster(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		roundTripNodePool(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		roundTripExternalAuth(t, original)
	}
}

// fuzzerFor can randomly populate api objects that are destined for version.
func fuzzerFor(funcs []interface{}, src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(0, 1)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(funcs...)
	return f
}

func roundTripHCPCluster(t *testing.T, original *api.HCPOpenShiftCluster) {
	v := version{}
	externalObj := v.NewHCPOpenShiftCluster(original)

	roundTrippedObj, err := externalObj.ConvertToInternal()
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, api.CmpDiffOptions...))
	}
}

func roundTripNodePool(t *testing.T, original *api.HCPOpenShiftClusterNodePool) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterNodePool(original)

	roundTrippedObj, err := externalObj.ConvertToInternal()
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, api.CmpDiffOptions...))
	}
}

func roundTripExternalAuth(t *testing.T, original *api.HCPOpenShiftClusterExternalAuth) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterExternalAuth(original)

	roundTrippedObj, err := externalObj.ConvertToInternal()
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, api.CmpDiffOptions...))
	}
}
