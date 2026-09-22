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
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
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
		// coreapi.CosmosMetadata: only compare ResourceID. CosmosETag is server-assigned and
		// ExistingCosmosUID is an in-memory bridge.
		func(a, b coreapi.CosmosMetadata) bool {
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
		// metadataapi.InternalID (value): compare by canonical path.
		func(a, b metadataapi.InternalID) bool {
			return a.Path() == b.Path()
		},
		// *metadataapi.InternalID (pointer): nil-safe path comparison.
		func(a, b *metadataapi.InternalID) bool {
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
			if bytes.Equal(aBytes, bBytes) {
				return true
			}
			// Fall back to a structural comparison so that key-ordering and
			// numeric-formatting differences (3 vs 3.0, 1e6 vs 1000000) don't
			// produce false positives, while keeping full integer precision so
			// that numbers beyond float64's 2^53 range stay distinguishable.
			aObj, err := decodeJSONPreservingNumbers(aBytes)
			if err != nil {
				return false
			}
			bObj, err := decodeJSONPreservingNumbers(bBytes)
			if err != nil {
				return false
			}
			return jsonValuesEqual(aObj, bObj)
		},
	); err != nil {
		panic(err)
	}
	return e
}()

// decodeJSONPreservingNumbers unmarshals JSON into a generic tree, keeping
// numbers as json.Number so that large integers retain full precision instead
// of collapsing onto the nearest float64.
func decodeJSONPreservingNumbers(data []byte) (any, error) {
	// json.Decoder.Decode accepts a valid JSON prefix and ignores trailing
	// tokens, whereas json.Unmarshal validates the whole input. Guard with
	// json.Valid so that malformed payloads (e.g. `{"n":1} garbage`) are
	// rejected instead of silently comparing equal and suppressing a write-back.
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// jsonValuesEqual compares two decoded JSON trees for semantic equality.
// Numbers are compared by numeric value (so 3, 3.0 and 3e0 are equal) while
// integers keep full int64 precision (so 2^54 and 2^54+1 stay distinct);
// objects are compared irrespective of key order.
func jsonValuesEqual(a, b any) bool {
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		return jsonNumbersEqual(av, bv)
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, avVal := range av {
			bvVal, ok := bv[k]
			if !ok || !jsonValuesEqual(avVal, bvVal) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		// Strings, bools, nil.
		return reflect.DeepEqual(a, b)
	}
}

// jsonNumbersEqual reports whether two JSON number literals represent the same
// numeric value. When both are integers they are compared as int64 so that
// values beyond float64's exact 2^53 range stay distinct (e.g. 2^54 vs 2^54+1);
// otherwise they are compared as float64 so that differently-formatted equal
// values match (3 == 3.0, 1e6 == 1000000). float64 parsing also neutralises
// pathological exponents such as 1e1000000000, which overflow to a parse error
// and fall back to an exact text comparison instead of allocating huge values.
func jsonNumbersEqual(a, b json.Number) bool {
	if ai, aerr := a.Int64(); aerr == nil {
		if bi, berr := b.Int64(); berr == nil {
			return ai == bi
		}
	}
	af, aerr := a.Float64()
	bf, berr := b.Float64()
	if aerr != nil || berr != nil {
		// Unparseable or out-of-range literal: fall back to exact text.
		return a.String() == b.String()
	}
	return af == bf
}

// ResourceIDsEqual compares two *azcorearm.ResourceID for equality by their
// canonical string form. Both may be nil; non-nil values are compared by
// String(), so independently-parsed instances with different parent pointer
// chains still compare equal when they represent the same ARM ID.
//
// The comparison is case-insensitive: ARM resource IDs are case-insensitive for
// their provider namespaces and resource types (for example Azure may return
// "Microsoft.RedHatOpenshift" where our internal types use
// "Microsoft.RedHatOpenShift"), so two IDs that differ only by casing represent
// the same resource and must compare equal.
func ResourceIDsEqual(a, b *azcorearm.ResourceID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.EqualFold(a.String(), b.String())
}

// NeedsUpdate reports whether `desired` differs from `existing` in any way that should cause us to
// write `desired` back to Cosmos. It is a strict-but-server-managed-fields-aware semantic equality
// check: all the fields that actually persist must match, but cosmos-managed values like the
// document etag are ignored, as are Go-level representation differences (RawExtension Raw vs
// Object, parent pointer chains in ResourceID, etc.).
func NeedsUpdate(existing, desired any) bool {
	return !needsUpdateEqualities.DeepEqual(existing, desired)
}
