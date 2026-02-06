// Copyright 2026 Microsoft Corporation
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

package api

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// deepCopyFuzzerFor creates a randfill.Filler suitable for filling API types
// that contain azcorearm.ResourceID and other types with private fields.
func deepCopyFuzzerFor(src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(1, 3)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			if c.Intn(100) < 5 {
				return
			}

			sub := Must(uuid.NewUUID()).String()
			resourceGroup := strings.ReplaceAll(c.String(10), "/", "-")
			*j = *Must(azcorearm.ParseResourceID("/subscriptions/" + sub + "/resourceGroups/" + resourceGroup + "/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster"))
		},
		func(j *arm.Resource, c randfill.Continue) {
			c.FillNoCustom(j)
			sub := Must(uuid.NewUUID()).String()
			resourceGroup := strings.ReplaceAll(c.String(10), "/", "-")
			j.ID = Must(azcorearm.ParseResourceID("/subscriptions/" + sub + "/resourceGroups/" + resourceGroup + "/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster"))
			j.Name = "myCluster"
			j.Type = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
		},
		func(j *HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			foo := Must(NewInternalID("/api/clusters_mgmt/v1/clusters/r" + strings.ReplaceAll(c.String(10), "/", "-")))
			j.ClusterServiceID = foo
		},
		func(j *HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			foo := Must(NewInternalID("/api/clusters_mgmt/v1/clusters/r" + strings.ReplaceAll(c.String(10), "/", "-")))
			j.ClusterServiceID = foo
		},
		func(j *Operation, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			foo := Must(NewInternalID("/api/clusters_mgmt/v1/clusters/r" + strings.ReplaceAll(c.String(10), "/", "-")))
			j.InternalID = foo
		},
		func(j *arm.ManagedServiceIdentity, c randfill.Continue) {
			c.FillNoCustom(j)
		},
	)
	return f
}

func TestDeepCopyHCPOpenShiftCluster(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := deepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		doDeepCopyTest(t, original, fuzzer)
	}
}

func TestDeepCopyHCPOpenShiftClusterNodePool(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := deepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		doDeepCopyTest(t, original, fuzzer)
	}
}

func TestDeepCopyOperation(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := deepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &Operation{}
		fuzzer.Fill(original)
		doDeepCopyTest(t, original, fuzzer)
	}
}

// doDeepCopyTest verifies that DeepCopyObject produces an independent copy.
// It encodes the original to JSON, then re-fuzzes the copy. If the copy and
// original share any references, re-fuzzing the copy would mutate the original,
// and the JSON encoding would differ.
func doDeepCopyTest(t *testing.T, original runtime.Object, fuzzer *randfill.Filler) {
	t.Helper()

	prefuzz, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal original: %v", err)
	}

	copied := original.DeepCopyObject()

	// Verify the copy matches the original.
	copiedJSON, err := json.Marshal(copied)
	if err != nil {
		t.Fatalf("failed to marshal copy: %v", err)
	}
	if !bytes.Equal(prefuzz, copiedJSON) {
		t.Errorf("DeepCopy did not preserve data:\n%s", cmp.Diff(string(prefuzz), string(copiedJSON)))
	}

	// Re-fuzz the copy. If any references are shared with the original,
	// this will modify the original through those shared references.
	fuzzer.Fill(copied)

	postfuzz, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal original after fuzzing copy: %v", err)
	}
	if !bytes.Equal(prefuzz, postfuzz) {
		t.Errorf("fuzzing copy modified original:\nbefore: %s\nafter:  %s", string(prefuzz), string(postfuzz))
	}
}
