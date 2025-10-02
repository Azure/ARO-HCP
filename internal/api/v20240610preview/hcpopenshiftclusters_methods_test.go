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
	"math/rand"
	"reflect"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/randfill"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestRoundTripInternalExternalInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		roundTripHCPCluster(t, original)
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

	roundTrippedObj := &api.HCPOpenShiftCluster{}
	externalObj.Normalize(roundTrippedObj)

	// useful for debugging
	//originalJSON, _ := json.MarshalIndent(original, "", "    ")
	//intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
	//resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
	//fmt.Printf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !reflect.DeepEqual(original, roundTrippedObj) {
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj))
	}
}
