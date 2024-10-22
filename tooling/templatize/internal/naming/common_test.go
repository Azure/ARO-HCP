package naming

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSuffixedName(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		prefix           string
		suffixDigestArgs []string
		maxLength        int
		suffixLength     int
		expected         string
		errorExpected    bool
	}{
		{
			name:             "no suffix",
			prefix:           "prefix",
			suffixDigestArgs: []string{},
			maxLength:        10,
			expected:         "prefix",
		},
		{
			name:             "no suffix - too long",
			prefix:           "prefix",
			suffixDigestArgs: []string{},
			maxLength:        4,
			expected:         "",
			errorExpected:    true,
		},
		{
			name:             "with suffix",
			prefix:           "prefix",
			suffixDigestArgs: []string{"arg1"},
			maxLength:        10,
			suffixLength:     3,
			expected:         "prefix-84f",
		},
		{
			name:             "with suffix - too long",
			prefix:           "prefix",
			suffixDigestArgs: []string{"arg1"},
			maxLength:        4,
			suffixLength:     3,
			expected:         "",
			errorExpected:    true,
		},
		{
			name:             "with multiple suffix args",
			prefix:           "prefix",
			suffixDigestArgs: []string{"arg1", "arg2"},
			maxLength:        10,
			suffixLength:     3,
			expected:         "prefix-cb9",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resourceName, err := suffixedName(testCase.prefix, "-", testCase.maxLength, testCase.suffixLength, testCase.suffixDigestArgs...)
			if testCase.errorExpected {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expected, resourceName)
		})
	}
}
