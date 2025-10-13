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

package conversion

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview"
)

var localEqualities = conversion.EqualitiesOrDie(
	func(a, b time.Time) bool {
		return a.UTC().Equal(b.UTC())
	},
	func(a, b *string) bool {
		// the conversions treat nil and empty as the same
		return ptr.Deref(a, "") == ptr.Deref(b, "")
	},
)

// fuzzerFor can randomly populate api objects that are destined for version.
func fuzzerFor(funcs []interface{}, src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(0, 1)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(funcs...)
	return f
}

// fuzz test to be sure that our non-reflective copyreadonlyvalues is equivalent
func TestCopyReadOnlyValues_Cluster(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 2000; i++ {
		t.Logf("iteration: %d", i)
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		copyReadOnlyCluster(t, original)
	}
}

func copyReadOnlyCluster(t *testing.T, originalInternal *api.HCPOpenShiftCluster) {
	originalExternal := v20240610preview.NewVersion().NewHCPOpenShiftCluster(originalInternal)
	oldDestinationExternal := v20240610preview.NewVersion().NewHCPOpenShiftCluster(&api.HCPOpenShiftCluster{})
	api.CopyReadOnlyValues(originalExternal, oldDestinationExternal)

	newDestinationInternal := &api.HCPOpenShiftCluster{}
	CopyReadOnlyClusterValues(newDestinationInternal, originalInternal)
	newDestinationExternal := v20240610preview.NewVersion().NewHCPOpenShiftCluster(newDestinationInternal)

	if !localEqualities.DeepEqual(oldDestinationExternal, newDestinationExternal) {
		oldStyleJSON, _ := json.MarshalIndent(oldDestinationExternal, "", "  ")
		newStyleJSON, _ := json.MarshalIndent(newDestinationExternal, "", "  ")
		t.Log(string(oldStyleJSON))
		t.Log(cmp.Diff(string(oldStyleJSON), string(newStyleJSON)))
		t.Error(cmp.Diff(oldDestinationExternal, newDestinationExternal))
	}
}
