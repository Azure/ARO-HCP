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

package database

import (
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestPrepareForCreate_SetsInstanceVersionToOne(t *testing.T) {
	obj := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{InstanceVersion: 0},
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
		obj := &arm.Subscription{
			CosmosMetadata: arm.CosmosMetadata{InstanceVersion: start},
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
	obj := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
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
	obj := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
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
	md := &arm.CosmosMetadata{}
	md.SetPartitionKey("MIXED-Case")
	if got, want := md.PartitionKey, "mixed-case"; got != want {
		t.Errorf("stored PartitionKey = %q, want %q", got, want)
	}
	if got, want := md.GetPartitionKey(), "mixed-case"; got != want {
		t.Errorf("GetPartitionKey() = %q, want %q", got, want)
	}
}
