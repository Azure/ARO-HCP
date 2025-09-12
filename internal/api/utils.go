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

package api

import (
	"encoding/json"
	"iter"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"dario.cat/mergo"
	jsonpatch "github.com/evanphx/json-patch"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	OpenShiftVersionPrefix = "openshift-v"
)

// Ptr returns a pointer to p.
func Ptr[T any](p T) *T {
	return &p
}

// Copied from Go's src/encoding/json/encode.go
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer, reflect.Struct:
		return v.IsZero()
	}
	return false
}

// PtrOrNil returns a pointer to p or nil if p is an empty value as
// would be determined by the "omitempty" option in json.Marshal.
func PtrOrNil[T any](p T) *T {
	if isEmptyValue(reflect.ValueOf(p)) {
		return nil
	}
	return &p
}

// TrimStringSlice returns a new string slice with all leading and
// trailing white space removed from each element and omitting any
// white-space-only elements.
func TrimStringSlice(s []string) []string {
	// Preserve nil in case it matters.
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, item := range s {
		item = strings.TrimSpace(item)
		if len(item) > 0 {
			out = append(out, item)
		}
	}
	return out
}

// DeleteNilsFromPtrSlice returns a slice with nil pointers removed.
func DeleteNilsFromPtrSlice[S ~[]*E, E any](s S) S {
	return slices.DeleteFunc(s, func(e *E) bool { return e == nil })
}

// NonNilSliceValues returns an iterator over index-value pairs in a slice
// of pointers in the usual order, but skipping over nils.
func NonNilSliceValues[E any](a []*E) iter.Seq2[int, *E] {
	return NonNilValues(slices.All(a))
}

// NonNilValues returns an iterator over a sequence of pairs of values that skips
// pairs where the second value in the pair is nil.
func NonNilValues[K any, V any](seq iter.Seq2[K, *V]) iter.Seq2[K, *V] {
	return func(yield func(K, *V) bool) {
		for k, v := range seq {
			if v != nil {
				if !yield(k, v) {
					return
				}
			}
		}
	}
}

// StringSliceToStringPtrSlice converts a slice of strings to a slice of string pointers.
func StringSliceToStringPtrSlice(s []string) []*string {
	// Preserve nil in case it matters.
	if s == nil {
		return nil
	}
	out := make([]*string, len(s))
	for index, item := range s {
		out[index] = Ptr(item)
	}
	return out
}

// StringPtrSliceToStringSlice converts a slice of string pointers to a slice of strings.
func StringPtrSliceToStringSlice(s []*string) []string {
	// Preserve nil in case it matters.
	if s == nil {
		return nil
	}
	s = DeleteNilsFromPtrSlice(s)
	out := make([]string, 0, len(s))
	for _, item := range s {
		out = append(out, *item)
	}
	return out
}

// StringMapToStringPtrMap converts a map of strings to a map of string pointers.
func StringMapToStringPtrMap(m map[string]string) map[string]*string {
	// Preserve nil in case it matters.
	if m == nil {
		return nil
	}
	out := make(map[string]*string, len(m))
	for key, val := range m {
		out[key] = Ptr(val)
	}
	return out
}

// StringPtrMapToStringMap converts a map of string pointers to a map of strings.
func StringPtrMapToStringMap(m map[string]*string) map[string]string {
	// Preserve nil in case it matters.
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for key, val := range m {
		if val != nil {
			out[key] = *val
		}
	}
	return out
}

// MergeStringPtrMap merges a map of string pointers into a map of strings
// following the rules of JSON merge-patch (RFC 7396). In particular, if a
// key in src has a nil value, that entry is deleted from dst. The function
// takes a pointer to the dst map in case the dst map is nil and needs to be
// initialized.
func MergeStringPtrMap(src map[string]*string, dst *map[string]string) {
	if src != nil && dst != nil {
		for key, val := range src {
			if val == nil {
				delete(*dst, key)
			} else {
				if *dst == nil {
					*dst = make(map[string]string)
				}
				(*dst)[key] = *val
			}
		}
	}
}

// CopyReadOnlyValues copies from src any empty values for read-only fields in dst.
func CopyReadOnlyValues[T any](src VersionedCreatableResource[T], dst VersionedCreatableResource[T]) {
	var recurse func(sv, dv reflect.Value, path string)

	// For nullable types, we don't know ahead of time if the element
	// type has any read-only fields. So we recurse with a test value
	// and if the test value comes back with a non-zero value, we set
	// dv to it.

	recurse = func(sv, dv reflect.Value, path string) {
		switch sv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !sv.IsNil() && dv.CanSet() {
				var elemPtr reflect.Value
				elemType := sv.Type().Elem()
				if dv.IsNil() {
					elemPtr = reflect.New(elemType)
				} else {
					elemPtr = dv
				}
				recurse(sv.Elem(), elemPtr.Elem(), path)
				if !elemPtr.Elem().IsZero() {
					dv.Set(elemPtr)
				}
			}

		case reflect.Map:
			if !sv.IsNil() && dv.CanSet() {
				var newMap reflect.Value
				elemType := sv.Type().Elem()
				if dv.IsNil() {
					newMap = reflect.MakeMap(sv.Type())
				} else {
					newMap = dv
				}

				iter := sv.MapRange()
				for iter.Next() {
					elemPtr := reflect.New(elemType)
					recurse(iter.Value(), elemPtr.Elem(), path)
					if !elemPtr.Elem().IsZero() {
						newMap.SetMapIndex(iter.Key(), elemPtr.Elem())
					}
				}

				if newMap.Len() > 0 {
					dv.Set(newMap)
				}
			}

		// FIXME Our current APIs don't include any read-only fields in
		//       slice elements, but this is how I THINK it would work.
		//       Needs more testing. Maybe just avoid this scenario.
		case reflect.Slice:
			if !sv.IsNil() && dv.CanSet() {
				var newSlice reflect.Value
				elemType := sv.Type().Elem()
				if dv.IsNil() {
					newSlice = reflect.MakeSlice(sv.Type(), 0, sv.Cap())
				} else {
					newSlice = dv
				}

				for i := 0; i < sv.Len(); i++ {
					elemPtr := reflect.New(elemType)
					recurse(sv.Index(i), elemPtr.Elem(), path)
					if !elemPtr.Elem().IsZero() {
						newSlice = reflect.Append(newSlice, elemPtr.Elem())
					}
				}

				if newSlice.Len() > 0 {
					dv.Set(newSlice)
				}
			}

		case reflect.Struct:
			var numExportedFields int

			for i := 0; i < sv.NumField(); i++ {
				structField := sv.Type().Field(i)

				if structField.IsExported() {
					numExportedFields++
					if structField.Anonymous {
						recurse(sv.Field(i), dv.Field(i), path)
					} else {
						recurse(sv.Field(i), dv.Field(i), join(path, structField.Name))
					}
				}
			}

			// Handle struct types with no exported fields as if
			// it were a scalar type. Talking to you, time.Time.
			if numExportedFields == 0 && dv.IsZero() && dv.CanSet() {
				flags, ok := src.GetVisibility(path)
				if ok && flags.ReadOnly() {
					dv.Set(sv)
				}
			}

		default:
			if dv.IsZero() && dv.CanSet() {
				flags, ok := src.GetVisibility(path)
				if ok && flags.ReadOnly() {
					dv.Set(sv)
				}
			}
		}
	}

	sv := reflect.ValueOf(src).Elem()
	dv := reflect.ValueOf(dst).Elem()
	if sv.Type() == dv.Type() {
		recurse(sv, dv, "")
	}
}

// ApplyRequestBody applies a JSON request body to the value pointed to by v.
// If the request method is PATCH, the request body is applied to v using JSON
// Merge Patch (RFC 7396) semantics. Otherwise the request body is unmarshalled
// directly to v.
func ApplyRequestBody(request *http.Request, body []byte, v any) *arm.CloudError {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return arm.NewInvalidRequestContentError(&json.InvalidUnmarshalError{Type: rv.Type()})
	}

	switch request.Method {
	case http.MethodPatch:
		originalData, err := json.Marshal(v)
		if err != nil {
			return arm.NewInternalServerError()
		}

		modifiedData, err := jsonpatch.MergePatch(originalData, body)
		if err != nil {
			return arm.NewInvalidRequestContentError(err)
		}

		// Reset *v to its zero value.
		rv.Elem().SetZero()

		err = json.Unmarshal(modifiedData, v)
		if err != nil {
			return arm.NewInvalidRequestContentError(err)
		}

	default:
		// We need to unmarshal in two phases because Unmarshal in
		// encoding/json (v1) replaces Go maps instead of merging JSON
		// keys into them. This is critical for UserAssignedIdentities.
		//
		// First we unmarshal the request body into a newly-allocated
		// struct of v's type, then merge the allocated struct into v.
		//
		// FIXME encoding/json/v2 claims to handle this better but is
		//       currently experimental. Its "Unmarshal" docs state:
		//
		//      "Maps are not cleared. If the Go map is nil, then a
		//       new map is allocated to decode into. If the decoded
		//       key matches an existing Go map entry, the entry
		//       value is reused by decoding the JSON object value
		//       into it."

		src := reflect.New(rv.Elem().Type()).Interface()

		err := json.Unmarshal(body, src)
		if err != nil {
			return arm.NewInvalidRequestContentError(err)
		}

		err = mergo.Merge(v, src, mergo.WithOverride)
		if err != nil {
			return arm.NewInternalServerError()
		}
	}

	return nil
}
