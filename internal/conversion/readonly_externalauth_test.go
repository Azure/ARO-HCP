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

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview"
)

// fuzz test to be sure that our non-reflective copyreadonlyvalues is equivalent
func TestCopyReadOnlyValues_ExternalAuth(t *testing.T) {
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
		original := &api.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		copyReadOnlyAuth(t, original)
	}
}

func copyReadOnlyAuth(t *testing.T, originalInternal *api.HCPOpenShiftClusterExternalAuth) {
	originalExternal := v20240610preview.NewVersion().NewHCPOpenShiftClusterExternalAuth(originalInternal)
	oldDestinationExternal := v20240610preview.NewVersion().NewHCPOpenShiftClusterExternalAuth(&api.HCPOpenShiftClusterExternalAuth{})
	api.CopyReadOnlyValues(originalExternal, oldDestinationExternal)

	newDestinationInternal := &api.HCPOpenShiftClusterExternalAuth{}
	CopyReadOnlyExternalAuthValues(newDestinationInternal, originalInternal)
	newDestinationExternal := v20240610preview.NewVersion().NewHCPOpenShiftClusterExternalAuth(newDestinationInternal)

	if !localEqualities.DeepEqual(oldDestinationExternal, newDestinationExternal) {
		oldStyleJSON, _ := json.MarshalIndent(oldDestinationExternal, "", "  ")
		newStyleJSON, _ := json.MarshalIndent(newDestinationExternal, "", "  ")
		t.Log(string(oldStyleJSON))
		t.Log(cmp.Diff(string(oldStyleJSON), string(newStyleJSON)))
		t.Error(cmp.Diff(oldDestinationExternal, newDestinationExternal))
	}
}
