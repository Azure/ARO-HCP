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

package v20261003preview

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestRoundTripInternalExternalInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := coreapitesting.FuzzerFor(
		coreapitesting.CommonRoundTripFuzzFuncs(),
		rand.NewSource(seed),
	)

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		// ConvertToInternal derives CosmosMetadata.ResourceID from arm.Resource.ID,
		// so synchronize them for a lossless round-trip comparison.
		original.ResourceID = original.ID
		// InstanceVersion does not roundtrip through the external type because
		// it is purely a database concern. The CosmosMetadata fuzz override
		// also zeroes this, but randfill does not always dispatch the custom
		// func when filling an embedded struct in-place, so zero it here too.
		original.InstanceVersion = 0
		// ActiveKey fields are no longer written by any API version; clear before round-trip.
		if original.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil && original.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
			original.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey = coreapi.KmsKey{}
		}
		roundTripHCPCluster(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		original.ResourceID = original.ID
		original.CosmosETag = ""
		original.InstanceVersion = 0
		roundTripNodePool(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		original.ResourceID = original.ID
		original.CosmosETag = ""
		original.InstanceVersion = 0
		roundTripExternalAuth(t, original)
	}
}

func roundTripHCPCluster(t *testing.T, original *coreapi.HCPOpenShiftCluster) {
	v := version{}
	externalObj := v.NewHCPOpenShiftCluster(original)

	roundTrippedObj, err := externalObj.ConvertToInternal(nil)
	require.NoError(t, err)

	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediate: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, coreapi.CmpDiffOptions...))
	}
}

func roundTripNodePool(t *testing.T, original *coreapi.HCPOpenShiftClusterNodePool) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterNodePool(original)

	roundTrippedObj, err := externalObj.ConvertToInternal(nil)
	require.NoError(t, err)

	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediate: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, coreapi.CmpDiffOptions...))
	}
}

func roundTripExternalAuth(t *testing.T, original *coreapi.HCPOpenShiftClusterExternalAuth) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterExternalAuth(original)

	roundTrippedObj, err := externalObj.ConvertToInternal(nil)
	require.NoError(t, err)

	if !equality.Semantic.DeepEqual(original, roundTrippedObj) {
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(roundTrippedObj, "", "    ")
		t.Logf("Original: %s\n\nIntermediate: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, roundTrippedObj, coreapi.CmpDiffOptions...))
	}
}
