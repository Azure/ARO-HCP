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
	"bytes"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// needsUpdateEqualities is a copy of equality.Semantic with extra equality functions for types
// that have multiple Go-level representations of the same persisted value, plus a CosmosMetadata
// equality that ignores the cosmos-managed CosmosETag and the in-memory-only ExistingCosmosUID.
//
// We need our own copy because equality.Semantic.DeepEqual sees two documents as different when:
//   - CosmosMetadata.CosmosETag is server-assigned on every write, so existing has a value and
//     desired typically does not (or has a different one).
//   - CosmosMetadata.ExistingCosmosUID is `json:"-"` and is filled in by the read conversion on
//     existing but is empty on a freshly-built desired.
//   - runtime.RawExtension can be carrying its data in either Raw or Object - reads populate Raw,
//     freshly-built desired values populate Object.
//   - *azcorearm.ResourceID has a parent pointer chain whose addresses differ between two
//     independently-parsed instances even though the represented ARM IDs are equal.
var needsUpdateEqualities = func() conversion.Equalities {
	e := equality.Semantic.Copy()
	if err := e.AddFuncs(
		// arm.CosmosMetadata: only compare ResourceID. CosmosETag is server-assigned and
		// ExistingCosmosUID is an in-memory bridge.
		func(a, b arm.CosmosMetadata) bool {
			return ResourceIDsEqual(a.ResourceID, b.ResourceID)
		},
		// *azcorearm.ResourceID: compare by string so unrelated parent pointer chains don't
		// cause spurious inequality.
		func(a, b *azcorearm.ResourceID) bool {
			return ResourceIDsEqual(a, b)
		},
		// azcorearm.ResourceID (value): same reason as the pointer form.
		func(a, b azcorearm.ResourceID) bool {
			return a.String() == b.String()
		},
		// api.InternalID (value): compare by canonical path.
		func(a, b api.InternalID) bool {
			return a.Path() == b.Path()
		},
		// *api.InternalID (pointer): nil-safe path comparison.
		func(a, b *api.InternalID) bool {
			if a == nil && b == nil {
				return true
			}
			if a == nil || b == nil {
				return false
			}
			return a.Path() == b.Path()
		},
		// runtime.RawExtension: compare via canonical JSON. RawExtension can carry data either as
		// Raw bytes or as a typed Object; both forms need to round-trip to the same JSON for our
		// purposes.
		func(a, b runtime.RawExtension) bool {
			aBytes, err := a.MarshalJSON()
			if err != nil {
				return false
			}
			bBytes, err := b.MarshalJSON()
			if err != nil {
				return false
			}
			return bytes.Equal(aBytes, bBytes)
		},
	); err != nil {
		panic(err)
	}
	return e
}()

// ResourceIDsEqual compares two *azcorearm.ResourceID for equality by their
// canonical string form. Both may be nil; non-nil values are compared by
// String(), so independently-parsed instances with different parent pointer
// chains still compare equal when they represent the same ARM ID.
func ResourceIDsEqual(a, b *azcorearm.ResourceID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}

// NeedsUpdate reports whether `desired` differs from `existing` in any way that should cause us to
// write `desired` back to Cosmos. It is a strict-but-server-managed-fields-aware semantic equality
// check: all the fields that actually persist must match, but cosmos-managed values like the
// document etag are ignored, as are Go-level representation differences (RawExtension Raw vs
// Object, parent pointer chains in ResourceID, etc.).
func NeedsUpdate(existing, desired any) bool {
	return !needsUpdateEqualities.DeepEqual(existing, desired)
}
