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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func TestMapping(t *testing.T) {
	testData := config.Configuration{
		"key1": "value1",
		"key2": 42,
		"key3": true,
		"parent": map[string]interface{}{
			"nested":    "nestedvalue",
			"nestedInt": 42,
			"deeper": map[string]interface{}{
				"deepest": "deepestvalue",
			},
		},
	}
	tests := []struct {
		name              string
		generator         PlaceholderGenerator
		expectedFlattened map[string]string
		expectedReplace   map[string]interface{}
	}{
		{
			name:      "dunder",
			generator: NewDunderPlaceholders(),
			expectedFlattened: map[string]string{
				"__key1__":                  "key1",
				"__key2__":                  "key2",
				"__key3__":                  "key3",
				"__parent.nested__":         "parent.nested",
				"__parent.nestedInt__":      "parent.nestedInt",
				"__parent.deeper.deepest__": "parent.deeper.deepest",
			},
			expectedReplace: map[string]interface{}{
				"key1": "__key1__",
				"key2": "__key2__",
				"key3": "__key3__",
				"parent": map[string]interface{}{
					"nested":    "__parent.nested__",
					"nestedInt": "__parent.nestedInt__",
					"deeper":    map[string]interface{}{"deepest": "__parent.deeper.deepest__"},
				},
			},
		},
		{
			name:      "bicep",
			generator: NewBicepParamPlaceholders(),
			expectedFlattened: map[string]string{
				"__key1__":                  "key1",
				"__key2__":                  "key2",
				"__key3__":                  "key3",
				"__parent.nested__":         "parent.nested",
				"__parent.nestedInt__":      "parent.nestedInt",
				"__parent.deeper.deepest__": "parent.deeper.deepest",
			},
			expectedReplace: map[string]interface{}{
				"key1": "__key1__",
				"key2": "any('__key2__')",
				"key3": "any('__key3__')",
				"parent": map[string]interface{}{
					"nested":    "__parent.nested__",
					"nestedInt": "any('__parent.nestedInt__')",
					"deeper":    map[string]interface{}{"deepest": "__parent.deeper.deepest__"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flattened, replace := EV2Mapping(testData, tc.generator, []string{})
			if diff := cmp.Diff(tc.expectedFlattened, flattened); diff != "" {
				t.Errorf("got incorrect flattened: %v", diff)
			}
			if diff := cmp.Diff(tc.expectedReplace, replace); diff != "" {
				t.Errorf("got incorrect replace: %v", diff)
			}
		})
	}
}

func TestPlaceholderGenerators(t *testing.T) {
	tests := []struct {
		name              string
		generator         PlaceholderGenerator
		key               []string
		valueType         reflect.Type
		expectedFlattened string
		expectedReplace   string
	}{
		{
			name:              "dunder",
			generator:         NewDunderPlaceholders(),
			key:               []string{"foo", "bar"},
			valueType:         nil,
			expectedFlattened: "__foo.bar__",
			expectedReplace:   "__foo.bar__",
		},
		{
			name:              "bicep string param",
			generator:         NewBicepParamPlaceholders(),
			key:               []string{"foo", "bar"},
			valueType:         reflect.TypeOf("baz"),
			expectedFlattened: "__foo.bar__",
			expectedReplace:   "__foo.bar__",
		},
		{
			name:              "bicep int param",
			generator:         NewBicepParamPlaceholders(),
			key:               []string{"foo", "bar"},
			valueType:         reflect.TypeOf(42),
			expectedFlattened: "__foo.bar__",
			expectedReplace:   "any('__foo.bar__')",
		},
		{
			name:              "bicep bool param",
			generator:         NewBicepParamPlaceholders(),
			key:               []string{"foo", "bar"},
			valueType:         reflect.TypeOf(true),
			expectedFlattened: "__foo.bar__",
			expectedReplace:   "any('__foo.bar__')",
		},
		{
			name:              "bicep array param",
			generator:         NewBicepParamPlaceholders(),
			key:               []string{"foo", "bar"},
			valueType:         reflect.TypeOf([]any{}),
			expectedFlattened: "__foo.bar__",
			expectedReplace:   "any('__foo.bar__')",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flattened, replace := tc.generator(tc.key, tc.valueType)
			if flattened != tc.expectedFlattened {
				t.Errorf("got incorrect flattened: %v", flattened)
			}
			if replace != tc.expectedReplace {
				t.Errorf("got incorrect replace: %v", replace)
			}
		})
	}
}
