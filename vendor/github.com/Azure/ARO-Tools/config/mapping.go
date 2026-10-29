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

package config

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Azure/ARO-Tools/config/types"
)

// PlaceholderGenerator produces a binding key and replacement text for a leaf.
type PlaceholderGenerator func(key []string, valueType reflect.Type) (flattenedKey string, replaceVar string)

// NewDunderPlaceholders returns a PlaceholderGenerator function that generates
// placeholder strings by joining the provided key elements with periods
// and surrounding them with double underscores.
//
// Example:
//
//	key := []string{"foo", "bar"}
//	flattenedKey, replaceVar := NewDunderPlaceholders()(key, nil)
//	// flattenedKey and replaceVar will both be "__foo.bar__"
func NewDunderPlaceholders() PlaceholderGenerator {
	return func(key []string, _ reflect.Type) (flattenedKey string, replaceVar string) {
		flattenedKey = fmt.Sprintf("__%s__", strings.Join(key, "."))
		replaceVar = flattenedKey
		return
	}
}

// EV2Mapping walks a nested config tree and returns:
//  1. a flat map of placeholder -> dotted key path,
//  2. a copy of the input where each leaf is replaced by a placeholder.
//
// The placeholder format is defined by placeholderGenerator and may use the
// leaf's reflect.Type. The prefix seeds the current traversal path. Only
// map[string]any values are traversed; other values, including lists, are leaves.
//
// Example:
//
//	input := types.Configuration{"ev2": map[string]any{"name": "svc"}}
//	flat, templ := EV2Mapping(input, NewDunderPlaceholders(), nil)
//	// flat["__ev2.name__"] == "ev2.name"
//	// templ["ev2"].(map[string]any)["name"] == "__ev2.name__"
func EV2Mapping(input types.Configuration, placeholderGenerator PlaceholderGenerator, prefix []string) (map[string]string, map[string]interface{}) {
	output := map[string]string{}
	replaced := map[string]interface{}{}
	for key, value := range input {
		nestedKey := append(prefix, key)
		nested, ok := value.(map[string]any)
		if ok {
			flattened, replacement := EV2Mapping(nested, placeholderGenerator, nestedKey)
			for index, what := range flattened {
				output[index] = what
			}
			replaced[key] = replacement
		} else {
			flattenedKey, replaceVar := placeholderGenerator(nestedKey, reflect.TypeOf(value))
			output[flattenedKey] = strings.Join(nestedKey, ".")
			replaced[key] = replaceVar
		}
	}
	return output, replaced
}
