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

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"

	"sigs.k8s.io/randfill"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestRoundTripInternalExternalInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := coreapitesting.FuzzerFor(append(coreapitesting.CommonRoundTripFuzzFuncs(),
		// ImageDigestMirrors, Ingress, and CryptoRestrictions do not exist in v20240610preview.
		func(j *coreapi.HCPOpenShiftClusterCustomerProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ImageDigestMirrors = nil
			j.Ingress = coreapi.CustomerIngressProfile{}
			j.CryptoRestrictions = metadataapi.CryptoRestrictionsNone
		},
		// VnetIntegrationSubnetID was added in v20251223preview and does not exist in v20240610preview.
		func(j *coreapi.CustomerPlatformProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.SubnetID != nil {
				j.SubnetID = coreapitesting.FuzzArmResourceID("Microsoft.Network/virtualNetworks/subnets", coreapitesting.GenName(c))
			}
			if j.NetworkSecurityGroupID != nil {
				j.NetworkSecurityGroupID = coreapitesting.FuzzArmResourceID("Microsoft.Network/networkSecurityGroups", coreapitesting.GenName(c))
			}
			j.VnetIntegrationSubnetID = nil
		},
		// DiskType was added in v20251223preview and does not exist in v20240610preview.
		func(j *coreapi.OSDiskProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.EncryptionSetID != nil {
				j.EncryptionSetID = coreapitesting.FuzzArmResourceID("Microsoft.Compute/diskEncryptionSets", coreapitesting.GenName(c))
			}
			j.DiskType = ""
		},
		// Visibility was added in v20251223preview and does not exist in v20240610preview.
		func(j *coreapi.KmsEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			j.Visibility = ""
		},
		func(j *coreapi.CustomerManagedEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			zeroValueKMS := coreapi.KmsEncryptionProfile{}
			if j.Kms != nil && *j.Kms == zeroValueKMS {
				j.Kms = nil
			}
		},
	), rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		original.ResourceID = original.ID
		original.InstanceVersion = 0
		original.PartitionKey = ""
		// KeyEncryptionKeyURL was added in v2026_09_01_preview and does
		// not round-trip through this version.
		if original.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil && original.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
			original.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyEncryptionKeyURL = ""
		}
		roundTripHCPCluster(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		original.ResourceID = original.ID
		original.CosmosETag = ""
		original.InstanceVersion = 0
		original.PartitionKey = ""
		roundTripNodePool(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &coreapi.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		original.ResourceID = original.ID
		original.CosmosETag = ""
		original.InstanceVersion = 0
		original.PartitionKey = ""
		roundTripExternalAuth(t, original)
	}
}

func roundTripHCPCluster(t *testing.T, original *coreapi.HCPOpenShiftCluster) {
	v := version{}
	externalObj := v.NewHCPOpenShiftCluster(original)

	roundTrippedObj, err := externalObj.ConvertToInternal(original)
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
