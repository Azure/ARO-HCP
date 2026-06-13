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

func TestNeedsUpdate_CosmosMetadata(t *testing.T) {
	ridStr := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"

	tests := []struct {
		name       string
		existing   arm.CosmosMetadata
		desired    arm.CosmosMetadata
		wantUpdate bool
	}{
		{
			name: "differing etags should not trigger an update",
			existing: arm.CosmosMetadata{
				ResourceID: mustParseRID(t, ridStr),
				CosmosETag: azcore.ETag("etag-1"),
			},
			desired: arm.CosmosMetadata{
				ResourceID: mustParseRID(t, ridStr),
				CosmosETag: azcore.ETag("etag-2"),
			},
			wantUpdate: false,
		},
		{
			name: "ExistingCosmosUID is internal-only and must not trigger an update",
			existing: arm.CosmosMetadata{
				ResourceID:        mustParseRID(t, ridStr),
				ExistingCosmosUID: "uid-from-cosmos",
			},
			desired: arm.CosmosMetadata{
				ResourceID: mustParseRID(t, ridStr),
			},
			wantUpdate: false,
		},
		{
			name: "different ResourceIDs must trigger an update",
			existing: arm.CosmosMetadata{
				ResourceID: mustParseRID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/a"),
			},
			desired: arm.CosmosMetadata{
				ResourceID: mustParseRID(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/b"),
			},
			wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantUpdate, NeedsUpdate(tt.existing, tt.desired))
		})
	}
}

func TestNeedsUpdate_ResourceID(t *testing.T) {
	ridStr := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"

	tests := []struct {
		name       string
		existing   *azcorearm.ResourceID
		desired    *azcorearm.ResourceID
		wantUpdate bool
	}{
		{
			name:       "same string form, distinct pointers, should not trigger an update",
			existing:   mustParseRID(t, ridStr),
			desired:    mustParseRID(t, ridStr),
			wantUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotSame(t, tt.existing, tt.desired, "test fixture: instances should be distinct pointers")
			assert.Equal(t, tt.wantUpdate, NeedsUpdate(tt.existing, tt.desired))
		})
	}
}

func TestNeedsUpdate_InternalID(t *testing.T) {
	idA, err := api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/abc")
	require.NoError(t, err)
	idB, err := api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/def")
	require.NoError(t, err)

	tests := []struct {
		name       string
		existing   any
		desired    any
		wantUpdate bool
	}{
		{
			name:       "value: same path should not trigger an update",
			existing:   idA,
			desired:    idA,
			wantUpdate: false,
		},
		{
			name:       "value: different paths must trigger an update",
			existing:   idA,
			desired:    idB,
			wantUpdate: true,
		},
		{
			name:       "pointer: non-nil vs nil must trigger an update",
			existing:   &idA,
			desired:    (*api.InternalID)(nil),
			wantUpdate: true,
		},
		{
			name:       "pointer: nil vs non-nil must trigger an update",
			existing:   (*api.InternalID)(nil),
			desired:    &idA,
			wantUpdate: true,
		},
		{
			name:       "pointer: nil vs nil should not trigger an update",
			existing:   (*api.InternalID)(nil),
			desired:    (*api.InternalID)(nil),
			wantUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantUpdate, NeedsUpdate(tt.existing, tt.desired))
		})
	}
}

func TestNeedsUpdate_NestedInternalIDPointer(t *testing.T) {
	idA, err := api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/abc")
	require.NoError(t, err)
	idB, err := api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/def")
	require.NoError(t, err)

	type wrapper struct {
		ShardID *api.InternalID
	}

	tests := []struct {
		name       string
		existing   wrapper
		desired    wrapper
		wantUpdate bool
	}{
		{
			name:       "same nested pointer should not trigger an update",
			existing:   wrapper{ShardID: &idA},
			desired:    wrapper{ShardID: &idA},
			wantUpdate: false,
		},
		{
			name:       "different nested pointers must trigger an update",
			existing:   wrapper{ShardID: &idA},
			desired:    wrapper{ShardID: &idB},
			wantUpdate: true,
		},
		{
			name:       "nil vs non-nil nested pointer must trigger an update",
			existing:   wrapper{ShardID: nil},
			desired:    wrapper{ShardID: &idA},
			wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantUpdate, NeedsUpdate(tt.existing, tt.desired))
		})
	}
}

func TestNeedsUpdate_RawExtension_NormalizesRawAndObject(t *testing.T) {
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

	bytes, err := json.Marshal(desired)
	require.NoError(t, err)
	roundTripped := &api.ManagementClusterContent{}
	require.NoError(t, json.Unmarshal(bytes, roundTripped))
	roundTripped.CosmosETag = azcore.ETag("server-assigned-etag")
	roundTripped.ExistingCosmosUID = "server-assigned-uid"

	assert.False(t, NeedsUpdate(roundTripped, desired), "round-tripped existing should compare equal to a freshly-built desired")
}
