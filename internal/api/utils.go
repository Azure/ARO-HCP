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
	"iter"
	"slices"
)

// Ptr returns a pointer to p.
func Ptr[T any](p T) *T {
	return &p
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
	out := make([]*string, len(s))
	for index, item := range s {
		out[index] = Ptr(item)
	}
	return out
}

// StringPtrSliceToStringSlice converts a slice of string pointers to a slice of strings.
func StringPtrSliceToStringSlice(s []*string) []string {
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
