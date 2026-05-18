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

package framework

import "reflect"

// matchSubset reports whether expected is a subset of actual, using the rules:
//
//   - Maps: every key in expected must exist in actual, and the corresponding
//     values must recursively matchSubset. Extra keys in actual are ignored.
//   - Slices: every element in expected must match (recursively) at least one
//     element in actual. Order is ignored. Extra elements in actual are
//     ignored. This matches how Kubernetes condition-like lists are usually
//     compared (the "Successful" condition is a member of the list, not the
//     whole list).
//   - Scalars: must be DeepEqual after JSON normalization.
//
// matchSubset is for asserting that a controller produced a desired field;
// it deliberately doesn't try to enforce "and nothing else." If a test
// needs that, it should add a length assertion alongside, or compare
// scalar-equal at a parent path.
func matchSubset(expected, actual any) bool {
	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range exp {
			av, ok := act[k]
			if !ok {
				return false
			}
			if !matchSubset(v, av) {
				return false
			}
		}
		return true

	case []any:
		act, ok := actual.([]any)
		if !ok {
			return false
		}
		for _, e := range exp {
			matched := false
			for _, a := range act {
				if matchSubset(e, a) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true

	default:
		return reflect.DeepEqual(expected, actual)
	}
}
