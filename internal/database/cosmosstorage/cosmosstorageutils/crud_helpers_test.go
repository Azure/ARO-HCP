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

package cosmosstorageutils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestPrepareParentResourceID(t *testing.T) {
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/SubscriptionID/resourceGroups/MyGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/MyCluster/nodePools/MyPool")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		resourceID *azcorearm.ResourceID
		want       string
	}{
		{
			name:       "nested mixed-case resource",
			resourceID: resourceID,
			want:       "/subscriptions/subscriptionid/resourcegroups/mygroup/providers/microsoft.redhatopenshift/hcpopenshiftclusters/mycluster",
		},
		{name: "nil resource ID"},
		{name: "nil parent", resourceID: &azcorearm.ResourceID{}},
	}
	for _, operation := range []string{"create", "replace"} {
		for _, test := range tests {
			for _, initial := range []string{"", "stale-parent"} {
				t.Run(operation+"/"+test.name+"/"+initial, func(t *testing.T) {
					metadata := &coreapi.CosmosMetadata{
						ResourceID:       test.resourceID,
						ParentResourceID: initial,
					}
					var prepareErr error
					if operation == "create" {
						prepareErr = PrepareForCreate(metadata)
					} else {
						metadata.InstanceVersion = 1
						metadata.CosmosETag = "etag"
						prepareErr = PrepareForReplace(metadata)
					}
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					if metadata.ParentResourceID != test.want {
						t.Errorf("ParentResourceID = %q, want %q", metadata.ParentResourceID, test.want)
					}
				})
			}
		}
	}
}

func TestPrepareParentResourceIDPreservesMetadataOnError(t *testing.T) {
	tests := []struct {
		name    string
		version int64
		etag    azcore.ETag
		prepare func(*coreapi.CosmosMetadata) error
	}{
		{name: "create with existing version", version: 1, prepare: PrepareForCreate[coreapi.CosmosMetadata]},
		{name: "replace without etag", version: 1, prepare: PrepareForReplace[coreapi.CosmosMetadata]},
		{name: "replace without version", etag: "etag", prepare: PrepareForReplace[coreapi.CosmosMetadata]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := coreapi.CosmosMetadata{
				ParentResourceID: "original-parent",
				InstanceVersion:  test.version,
				CosmosETag:       test.etag,
			}
			original := metadata
			if err := test.prepare(&metadata); err == nil {
				t.Fatal("expected preparation to fail")
			}
			if metadata != original {
				t.Errorf("metadata mutated on error: got %#v, want %#v", metadata, original)
			}
		})
	}
}

func TestCosmosMetadataParentResourceIDJSONRoundTrip(t *testing.T) {
	for _, parent := range []string{"", "/subscriptions/subscriptionid/resourcegroups/mygroup"} {
		t.Run(parent, func(t *testing.T) {
			metadata := coreapi.CosmosMetadata{ParentResourceID: parent}
			data, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["parentResourceID"]; present != (parent != "") {
				t.Errorf("parentResourceID presence = %t, want %t: %s", present, parent != "", data)
			}
			var roundTrip coreapi.CosmosMetadata
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if roundTrip.ParentResourceID != parent {
				t.Errorf("round-trip parent = %q, want %q", roundTrip.ParentResourceID, parent)
			}
		})
	}
}

func TestPrepareForCreate_SetsInstanceVersionToOne(t *testing.T) {
	obj := &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{InstanceVersion: 0},
	}
	if err := PrepareForCreate(obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.InstanceVersion != 1 {
		t.Errorf("got InstanceVersion=%d, want 1", obj.InstanceVersion)
	}
}

func TestPrepareForCreate_RejectsNonZeroInstanceVersion(t *testing.T) {
	for _, start := range []int64{1, 7, 999} {
		obj := &coreapi.Subscription{
			CosmosMetadata: coreapi.CosmosMetadata{InstanceVersion: start},
		}
		err := PrepareForCreate(obj)
		if err == nil {
			t.Errorf("starting InstanceVersion=%d: expected error, got nil", start)
			continue
		}
		if !strings.Contains(err.Error(), "InstanceVersion to be 0") {
			t.Errorf("starting InstanceVersion=%d: error should mention the InstanceVersion requirement; got: %v", start, err)
		}
		if obj.InstanceVersion != start {
			t.Errorf("starting InstanceVersion=%d: object was mutated on the failure path: got %d", start, obj.InstanceVersion)
		}
	}
}

func TestPrepareForReplace_IncrementsInstanceVersion(t *testing.T) {
	obj := &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			InstanceVersion: 7,
			CosmosETag:      azcore.ETag("etag-7"),
		},
	}
	if err := PrepareForReplace(obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.InstanceVersion != 8 {
		t.Errorf("got InstanceVersion=%d, want 8", obj.InstanceVersion)
	}
}

func TestPrepareForReplace_RequiresEtag(t *testing.T) {
	obj := &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			InstanceVersion: 7,
			// CosmosETag intentionally empty
		},
	}
	err := PrepareForReplace(obj)
	if err == nil {
		t.Fatal("expected an error for missing etag, got nil")
	}
	if !strings.Contains(err.Error(), "non-empty CosmosETag") {
		t.Errorf("error should mention the etag requirement; got: %v", err)
	}
	// InstanceVersion must not have been touched on the failure path —
	// otherwise a caller that swallows the error would silently double-bump
	// on the next retry.
	if obj.InstanceVersion != 7 {
		t.Errorf("InstanceVersion was mutated on the failure path: got %d, want 7", obj.InstanceVersion)
	}
}

func TestSetPartitionKeyLowercases(t *testing.T) {
	md := &coreapi.CosmosMetadata{}
	md.SetPartitionKey("MIXED-Case")
	if got, want := md.PartitionKey, "mixed-case"; got != want {
		t.Errorf("stored PartitionKey = %q, want %q", got, want)
	}
	if got, want := md.GetPartitionKey(), "mixed-case"; got != want {
		t.Errorf("GetPartitionKey() = %q, want %q", got, want)
	}
}
