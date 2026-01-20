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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnyToString(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"string", "foo", "foo"},
		{"int", 42, "42"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
		{"float64", 3.14, "3.14"},
		{"float32", float32(2.71), "2.71"},
		{"map_string_string", map[string]string{"a": "b"}, `{"a":"b"}`},
		{"map_string_any", map[string]any{"x": 1, "y": true}, `{"x":1,"y":true}`},
		{"slice_int", []int{1, 2, 3}, `[1,2,3]`},
		{"slice_string", []string{"a", "b"}, `["a","b"]`},
		{"struct", struct {
			X int
			Y string
		}{X: 1, Y: "z"}, `{"X":1,"Y":"z"}`},
		{"nil", nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, AnyToString(tt.value))
		})
	}
}
