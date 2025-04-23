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

package internal

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestFilterTagsToSync(t *testing.T) {
	testCase := []struct {
		name     string
		src      []string
		target   []string
		expected []string
	}{
		{
			name:     "empty source",
			src:      []string{},
			expected: []string{},
		},
		{
			name:     "new tags",
			src:      []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:   "no new tags",
			src:    []string{"a", "b", "c"},
			target: []string{"a", "b", "c"},
		},
		{
			name:     "one new tag",
			src:      []string{"a", "b", "c"},
			target:   []string{"a", "b"},
			expected: []string{"c"},
		},
	}

	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			tagsToSync := filterTagsToSync(tc.src, tc.target)
			assert.Equal(t, len(tc.expected), len(tagsToSync))
			for _, tag := range tc.expected {
				found := false
				for _, tagToSync := range tagsToSync {
					if tag == tagToSync {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("tag %s not found in tagsToSync", tag)
				}
			}

		})
	}

}
