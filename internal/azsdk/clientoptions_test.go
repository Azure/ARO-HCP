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

package azsdk

import "testing"

func TestFirstN(t *testing.T) {
	tests := []struct {
		name     string
		str      string
		n        int
		expected string
	}{
		{
			name:     "string shorter than n",
			str:      "hello",
			n:        10,
			expected: "hello",
		},
		{
			name:     "string equal to n",
			str:      "hello",
			n:        5,
			expected: "hello",
		},
		{
			name:     "string longer than n",
			str:      "hello world",
			n:        5,
			expected: "hello",
		},
		{
			name:     "empty string",
			str:      "",
			n:        5,
			expected: "",
		},
		{
			name:     "n is zero",
			str:      "hello",
			n:        0,
			expected: "",
		},
		{
			name:     "string with unicode characters",
			str:      "héllo wörld",
			n:        5,
			expected: "héllo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstN(tt.str, tt.n)
			if result != tt.expected {
				t.Errorf("firstN(%q, %d) = %q, expected %q", tt.str, tt.n, result, tt.expected)
			}
		})
	}
}
