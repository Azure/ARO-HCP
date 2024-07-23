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
