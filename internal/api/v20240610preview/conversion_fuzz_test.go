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
		func(j *api.HCPOpenShiftClusterCustomerProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ImageDigestMirrors is a v20251223preview field that does not exist in v20240610preview.
			// It cannot roundtrip through this version's external type.
			// Cross-version preservation is handled by ZeroOwnedFields not zeroing it.
			j.ImageDigestMirrors = nil
		},
		func(j *api.HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ActiveOperationID does not roundtrip through the external type because it is purely an internal detail
			j.ActiveOperationID = ""
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ocm.InternalID{}
			j.ExistingCosmosUID = ""
		},
		func(j *api.OSDiskProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			// DiskType is a v20251223preview field that does not exist in v20240610preview.
			// It cannot roundtrip through this version's external type.
			// Cross-version preservation is handled by ZeroOwnedFields not zeroing it.
			j.DiskType = ""
		},
		func(j *api.HCPOpenShiftClusterExternalAuthServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ActiveOperationID does not roundtrip through the external type because it is purely an internal detail
			j.ActiveOperationID = ""
			// ClusterServiceID does not roundtrip through the external type because it is purely an internal detail
			j.ClusterServiceID = ocm.InternalID{}
			j.ExistingCosmosUID = ""
		},
		func(j *api.HCPOpenShiftClusterExternalAuthProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ProvisioningState is service-provider-only and is NOT mapped by ApplyOwnedFields.
			j.ProvisioningState = ""
			// Condition is service-provider-only (@visibility(Lifecycle.Read)) and is NOT mapped by ApplyOwnedFields.
			j.Condition = api.ExternalAuthCondition{}
		},
		func(j *api.HCPOpenShiftClusterNodePoolProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			// ProvisioningState is service-provider-only and is NOT mapped by ApplyOwnedFields.
			j.ProvisioningState = ""
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
			// ManagedIdentitiesDataPlaneIdentityURL does not roundtrip through the external type because
			// the information is not provided in the request body. That information is provided via
			// the http header 'X-Ms-Identity-Url' and we set it after the call to conversion to internal.
			j.ManagedIdentitiesDataPlaneIdentityURL = ""
			// ProvisioningState is service-provider-only and is NOT mapped by ApplyOwnedFields.
			// It is managed by CopyReadOnlyClusterValues after conversion.
			j.ProvisioningState = ""
		},
		func(j *api.CustomerPlatformProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			// VnetIntegrationSubnetID was added in v2025_12_23_preview and does not exist in v2024_06_10_preview
			j.VnetIntegrationSubnetID = nil
		},
		func(j *api.KmsEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			// Visibility was added in v2025_12_23_preview and does not exist in v2024_06_10_preview
			j.Visibility = ""
		},
		func(j *api.CustomerManagedEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			// A zero-value KmsEncryptionProfile cannot roundtrip because
			// PtrOrNil collapses the all-zero KmsKey to nil on the external
			// type, causing normalizeCustomerManaged to skip Kms entirely
			// (its p.Kms.ActiveKey != nil guard is false).
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
		// CosmosETag does not roundtrip through the external type because it is purely a database concern
		original.CosmosETag = ""
		roundTripHCPCluster(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		// CosmosETag does not roundtrip through the external type because it is purely a database concern
		original.CosmosETag = ""
		roundTripNodePool(t, original)
	}

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftClusterExternalAuth{}
		fuzzer.Fill(original)
		// CosmosETag does not roundtrip through the external type because it is purely a database concern
		original.CosmosETag = ""
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

// roundTripHCPCluster verifies the overlay+reset pattern for v20240610preview:
// internal → external → ApplyVersionedUpdate(original.DeepCopy()) → compare.
//
// Using ApplyVersionedUpdate (not ApplyVersionedCreate) is intentional: v20240610preview
// does not own v20251223preview-exclusive fields (ImageDigestMirrors, VnetIntegrationSubnetID,
// Kms.Visibility). The overlay semantics require that those fields are preserved from the
// "existing" doc. Starting from original.DeepCopy() provides that existing doc, so the
// unknown-to-v2024 fields survive the round trip.
func roundTripHCPCluster(t *testing.T, original *api.HCPOpenShiftCluster) {
	v := version{}
	externalObj := v.NewHCPOpenShiftCluster(original)

	base := original.DeepCopy()
	err := api.ApplyVersionedUpdate(externalObj.(*HcpOpenShiftCluster), base)
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, base) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(base, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, base, api.CmpDiffOptions...))
	}
}

func roundTripNodePool(t *testing.T, original *api.HCPOpenShiftClusterNodePool) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterNodePool(original)

	base := original.DeepCopy()
	err := api.ApplyVersionedUpdate(externalObj.(*NodePool), base)
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, base) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(base, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, base, api.CmpDiffOptions...))
	}
}

func roundTripExternalAuth(t *testing.T, original *api.HCPOpenShiftClusterExternalAuth) {
	v := version{}
	externalObj := v.NewHCPOpenShiftClusterExternalAuth(original)

	base := original.DeepCopy()
	err := api.ApplyVersionedUpdate(externalObj.(*ExternalAuth), base)
	require.NoError(t, err)

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, base) {
		// useful for debugging
		originalJSON, _ := json.MarshalIndent(original, "", "    ")
		intermediateJSON, _ := json.MarshalIndent(externalObj, "", "    ")
		resultJSON, _ := json.MarshalIndent(base, "", "    ")
		t.Logf("Original: %s\n\nIntermediat: %s\n\n result: %s\n\n", string(originalJSON), string(intermediateJSON), string(resultJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, base, api.CmpDiffOptions...))
	}
}
