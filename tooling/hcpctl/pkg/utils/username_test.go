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
	"strings"
	"testing"
)

func TestSanitizeUsername(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		expected  string
		expectErr bool
	}{
		{
			name:      "empty username",
			username:  "",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "simple lowercase username",
			username:  "john",
			expected:  "john",
			expectErr: false,
		},
		{
			name:      "simple uppercase username",
			username:  "JOHN",
			expected:  "john",
			expectErr: false,
		},
		{
			name:      "mixed case username",
			username:  "JohnDoe",
			expected:  "johndoe",
			expectErr: false,
		},
		{
			name:      "username with numbers",
			username:  "john123",
			expected:  "john123",
			expectErr: false,
		},
		{
			name:      "username with hyphen",
			username:  "john-doe",
			expected:  "john-doe",
			expectErr: false,
		},
		{
			name:      "username with underscore",
			username:  "john_doe",
			expected:  "john-doe",
			expectErr: false,
		},
		{
			name:      "username with special characters",
			username:  "john@example.com",
			expected:  "john-example-com",
			expectErr: false,
		},
		{
			name:      "username with spaces",
			username:  "john doe",
			expected:  "john-doe",
			expectErr: false,
		},
		{
			name:      "username with leading/trailing special chars",
			username:  "@john.doe@",
			expected:  "john-doe",
			expectErr: false,
		},
		{
			name:      "username with multiple consecutive special chars",
			username:  "john...doe",
			expected:  "john---doe",
			expectErr: false,
		},
		{
			name:      "very long username",
			username:  strings.Repeat("a", 100),
			expected:  strings.Repeat("a", 63),
			expectErr: false,
		},
		{
			name:      "long username ending with hyphen after truncation",
			username:  strings.Repeat("a", 62) + "-" + strings.Repeat("b", 10),
			expected:  strings.Repeat("a", 62),
			expectErr: false,
		},
		{
			name:      "username with only special characters",
			username:  "!@#$%^&*()",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "username with leading/trailing hyphens after conversion",
			username:  "---john---",
			expected:  "john",
			expectErr: false,
		},
		{
			name:      "email address",
			username:  "john.doe@microsoft.com",
			expected:  "john-doe-microsoft-com",
			expectErr: false,
		},
		{
			name:      "domain\\username format",
			username:  "DOMAIN\\johndoe",
			expected:  "domain-johndoe",
			expectErr: false,
		},
		{
			name:      "unicode characters",
			username:  "john-李明",
			expected:  "john",
			expectErr: false,
		},
		{
			name:      "only unicode characters",
			username:  "李明",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "numeric username",
			username:  "12345",
			expected:  "12345",
			expectErr: false,
		},
		{
			name:      "single character",
			username:  "a",
			expected:  "a",
			expectErr: false,
		},
		{
			name:      "single special character",
			username:  "@",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizeUsername(tt.username)

			if tt.expectErr {
				if err == nil {
					t.Errorf("SanitizeUsername(%q) expected error but got none", tt.username)
				}
			} else {
				if err != nil {
					t.Errorf("SanitizeUsername(%q) unexpected error: %v", tt.username, err)
				}
				if result != tt.expected {
					t.Errorf("SanitizeUsername(%q) = %q, want %q", tt.username, result, tt.expected)
				}
			}
		})
	}
}
