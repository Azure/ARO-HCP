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

package ev2

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Azure/ARO-Tools/pkg/config"
)

type PlaceholderGenerator func(key []string, valueType reflect.Type) (flattenedKey string, replaceVar string)

// NewDunderPlaceholders returns a PlaceholderGenerator function that generates
// placeholder strings by joining the provided key elements with underscores
// and surrounding them with double underscores.
//
// Example:
//
//	key := []string{"foo", "bar"}
//	flattenedKey, replaceVar := NewDunderPlaceholders()(key, nil)
//	// flattenedKey and replaceVar will both be "__foo_bar__"
func NewDunderPlaceholders() PlaceholderGenerator {
	return func(key []string, _ reflect.Type) (flattenedKey string, replaceVar string) {
		flattenedKey = fmt.Sprintf("__%s__", strings.Join(key, "."))
		replaceVar = flattenedKey
		return
	}
}

// NewBicepParamPlaceholders creates a new PlaceholderGenerator that generates
// placeholders for Bicep parameters. It uses DunderPlaceholders to generate
// the initial placeholders and then wraps non-string values with the "any()"
// function for general EV2 bicep happyness.
//
// Returns:
//
//	A PlaceholderGenerator function that takes a key and value, and returns
//	a flattened key and a replacement variable for bicep parameter usage within EV2.
func NewBicepParamPlaceholders() PlaceholderGenerator {
	dunder := NewDunderPlaceholders()
	return func(key []string, valueType reflect.Type) (flattenedKey string, replaceVar string) {
		flattenedKey, replaceVar = dunder(key, valueType)
		if valueType.Kind() != reflect.String {
			replaceVar = fmt.Sprintf("any('%s')", replaceVar)
		}
		return
	}
}

func EV2Mapping(input config.Configuration, placeholderGenerator PlaceholderGenerator, prefix []string) (map[string]string, map[string]interface{}) {
	vars, _ := config.InterfaceToConfiguration(input)
	output := map[string]string{}
	replaced := map[string]interface{}{}
	for key, value := range vars {
		nestedKey := append(prefix, key)
		nested, ok := value.(config.Configuration)
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
