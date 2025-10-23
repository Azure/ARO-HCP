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

package v20251223preview

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
)

func TestRoundTripInternalExternalInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *api.HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ""
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		roundTripInternalHCPCluster(t, original)
	}
}

func TestRoundTripExternalInternalExternal(t *testing.T) {
	t.Skip("zero value pointer don't roundtrip, making the comparison impossible until we fix that.")
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *HcpOpenShiftCluster, c randfill.Continue) {
			c.FillNoCustom(j)

			// TODO determine if this is intentional (required fields only and correctly reported) or accidental.
			if j.Properties == nil {
				newVal := &generated.HcpOpenShiftClusterProperties{}
				c.Fill(newVal)
				j.Properties = newVal
			}
			if j.ID == nil {
				newVal := ptr.To("")
				c.Fill(&newVal)
				j.ID = newVal
			}
		},
		func(j *generated.HcpOpenShiftClusterProperties, c randfill.Continue) {
			c.FillNoCustom(j)

			// zero values don't currently roundtrip.  This is a problem for expressivity and patching
			// TODO determine if this is intentional (required fields only and correctly reported) or accidental.
			if j.API != nil && reflect.DeepEqual(*j.API, generated.APIProfile{}) {
				j.API = nil
			}
			if j.ClusterImageRegistry != nil && reflect.DeepEqual(*j.ClusterImageRegistry, generated.ClusterImageRegistryProfile{}) {
				j.ClusterImageRegistry = nil
			}
			if j.DNS != nil && reflect.DeepEqual(*j.DNS, generated.DNSProfile{}) {
				j.DNS = nil
			}
			if j.Etcd != nil && reflect.DeepEqual(*j.Etcd, generated.EtcdProfile{}) {
				j.Etcd = nil
			}
			if j.Console != nil && reflect.DeepEqual(*j.Console, generated.ConsoleProfile{}) {
				j.Console = nil
			}
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &HcpOpenShiftCluster{}
		fuzzer.Fill(original)
		roundTripExternalHCPCluster(t, original)
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

func roundTripInternalHCPCluster(t *testing.T, original *api.HCPOpenShiftCluster) {
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

func roundTripExternalHCPCluster(t *testing.T, original *HcpOpenShiftCluster) {
	v := version{}
	internalObj := &api.HCPOpenShiftCluster{}
	original.Normalize(internalObj)

	roundTrippedObj := v.NewHCPOpenShiftCluster(internalObj)

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
