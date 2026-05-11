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

package controllerutils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func mustParseRID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(s)
	require.NoError(t, err)
	return rid
}

func TestNeedsUpdate_CosmosMetadata_IgnoresEtag(t *testing.T) {
	ridStr := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"
	a := arm.CosmosMetadata{
		ResourceID: mustParseRID(t, ridStr),
		CosmosETag: azcore.ETag("etag-1"),
	}
	b := arm.CosmosMetadata{
		ResourceID: mustParseRID(t, ridStr),
		CosmosETag: azcore.ETag("etag-2"),
	}
	assert.False(t, NeedsUpdate(a, b), "differing etags should not trigger an update")
}

func TestNeedsUpdate_CosmosMetadata_IgnoresExistingCosmosUID(t *testing.T) {
	ridStr := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"
	a := arm.CosmosMetadata{
		ResourceID:        mustParseRID(t, ridStr),
		ExistingCosmosUID: "uid-from-cosmos",
	}
	b := arm.CosmosMetadata{
		ResourceID: mustParseRID(t, ridStr),
		// freshly built desired - no UID yet
	}
	assert.False(t, NeedsUpdate(a, b), "ExistingCosmosUID is internal-only and must not trigger an update")
}

func TestNeedsUpdate_CosmosMetadata_DetectsResourceIDDifference(t *testing.T) {
	a := arm.CosmosMetadata{
		ResourceID: mustParseRID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/a"),
	}
	b := arm.CosmosMetadata{
		ResourceID: mustParseRID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/b"),
	}
	assert.True(t, NeedsUpdate(a, b), "different ResourceIDs must trigger an update")
}

func TestNeedsUpdate_ResourceID_ComparedByString(t *testing.T) {
	// Two independently-parsed ResourceIDs share string form but have different parent pointer
	// chains. equality.Semantic would report them as different; NeedsUpdate must not.
	ridStr := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"
	a := mustParseRID(t, ridStr)
	b := mustParseRID(t, ridStr)
	assert.NotSame(t, a, b, "test fixture: instances should be distinct pointers")
	assert.False(t, NeedsUpdate(a, b), "ResourceIDs with the same string form should not trigger an update")
}

func TestNeedsUpdate_RawExtension_NormalizesRawAndObject(t *testing.T) {
	// runtime.RawExtension can carry data either in Raw bytes or as a typed Object. Both forms
	// produce identical persisted JSON, so they must compare equal for our purposes.
	hc := map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata":   map[string]any{"name": "hc1", "namespace": "ns1"},
	}
	rawBytes, err := json.Marshal(hc)
	require.NoError(t, err)

	rawForm := runtime.RawExtension{Raw: rawBytes}
	objForm := runtime.RawExtension{Object: &unstructured.Unstructured{Object: hc}}

	assert.False(t, NeedsUpdate(rawForm, objForm), "Raw and Object forms with the same content must be equal")
}

func TestNeedsUpdate_ManagementClusterContent_RoundTripUnchanged(t *testing.T) {
	// End-to-end check: a ManagementClusterContent built fresh and then marshalled+unmarshalled
	// (the path through cosmos read) must compare as not needing an update.
	rid := mustParseRID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c/managementClusterContents/readonlyHypershiftHostedCluster")
	desired := &api.ManagementClusterContent{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: rid},
		Status: api.ManagementClusterContentStatus{
			Conditions: []metav1.Condition{
				{
					Type:    "Degraded",
					Status:  metav1.ConditionFalse,
					Reason:  "NoErrors",
					Message: "As expected.",
				},
			},
			KubeContent: &metav1.List{
				Items: []runtime.RawExtension{
					{Object: &unstructured.Unstructured{Object: map[string]any{
						"apiVersion": "hypershift.openshift.io/v1beta1",
						"kind":       "HostedCluster",
						"metadata":   map[string]any{"name": "hc1"},
					}}},
				},
			},
		},
	}

	// Round-trip through JSON to mimic the read path which fills RawExtension.Raw and assigns a
	// new etag/UID.
	bytes, err := json.Marshal(desired)
	require.NoError(t, err)
	roundTripped := &api.ManagementClusterContent{}
	require.NoError(t, json.Unmarshal(bytes, roundTripped))
	roundTripped.CosmosETag = azcore.ETag("server-assigned-etag")
	roundTripped.ExistingCosmosUID = "server-assigned-uid"

	assert.False(t, NeedsUpdate(roundTripped, desired), "round-tripped existing should compare equal to a freshly-built desired")
}
