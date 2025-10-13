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
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
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

func TestHCPClusterJSONRoundTripThroughResourceDocument(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *TypedDocument, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ResourceType = api.ClusterResourceType.String()
		},
		func(j *map[string]any, c randfill.Continue) {
		},
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *ocm.InternalID, c randfill.Continue) {
			*j = api.Must(ocm.NewInternalID("/api/clusters_mgmt/v1/clusters/fixed-value"))
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &HCPCluster{}
		roundTrippedObj := &HCPCluster{}
		fuzzer.Fill(original)
		roundTrip(t, original, roundTrippedObj, FilterHCPClusterState)
	}
}

func roundTrip(t *testing.T, original, roundTrippedObj any, documentFilter ResourceDocumentStateFilter) {
	originalJSON, err := json.MarshalIndent(original, "", "    ")
	if err != nil {
		t.Fatalf("failed to marshal original: %v", err)
	}
	// useful for debugging
	//t.Log(string(originalJSON))

	typedDocument, resourceDocument, err := typedDocumentUnmarshal[ResourceDocument](originalJSON)
	if err != nil {
		t.Fatalf("failed to unmarshal into typedDocument: %v", err)
	}

	resourceDocumentJSON, err := resourceDocumentMarshal(typedDocument, resourceDocument, documentFilter)
	if err != nil {
		t.Fatalf("failed to marshal ResourceDocument: %v", err)
	}

	err = json.Unmarshal(resourceDocumentJSON, roundTrippedObj)
	if err != nil {
		t.Fatalf("failed to unmarshal into roundTrippedObj: %v", err)
	}

	roundTrippedJSON, err := json.MarshalIndent(roundTrippedObj, "", "    ")
	if err != nil {
		t.Fatalf("failed to marshal roundTrippedObj: %v", err)
	}

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !reflect.DeepEqual(originalJSON, roundTrippedJSON) {
		t.Logf("originalJSON\n%s", string(originalJSON))
		t.Logf("resourceDocumentJSON\n%s", string(resourceDocumentJSON))
		t.Logf("roundTrippedJSON\n%s", string(roundTrippedJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(originalJSON, roundTrippedJSON))
	}
}
