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

	"k8s.io/apimachinery/pkg/util/validation"
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

				// Property-based validation: result must be valid DNS-1123 label
				if errs := validation.IsDNS1123Label(result); len(errs) > 0 {
					t.Errorf("SanitizeUsername(%q) = %q is not a valid DNS-1123 label: %v", tt.username, result, errs)
				}
			}
		})
	}
}

// TestSanitizeUsernameProperties tests invariant properties of SanitizeUsername
func TestSanitizeUsernameProperties(t *testing.T) {
	tests := []struct {
		name     string
		username string
	}{
		{"simple", "john"},
		{"with special chars", "john@example.com"},
		{"long input", strings.Repeat("a", 100)},
		{"unicode", "john-李明"},
		{"mixed case", "JohnDoe"},
		{"numbers", "user123"},
		{"complex", "DOMAIN\\user.name@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizeUsername(tt.username)
			if err != nil {
				return // Skip error cases for property testing
			}

			// Property 1: Result should be deterministic
			result2, err2 := SanitizeUsername(tt.username)
			if err2 != nil || result != result2 {
				t.Errorf("SanitizeUsername is not deterministic for %q: %q vs %q", tt.username, result, result2)
			}

			// Property 2: Result should be valid DNS-1123 label
			if errs := validation.IsDNS1123Label(result); len(errs) > 0 {
				t.Errorf("SanitizeUsername(%q) = %q is not a valid DNS-1123 label: %v", tt.username, result, errs)
			}

			// Property 3: Length constraints
			if len(result) < 1 || len(result) > 63 {
				t.Errorf("SanitizeUsername(%q) = %q has invalid length %d", tt.username, result, len(result))
			}

			// Property 4: Character constraints
			for _, r := range result {
				if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
					t.Errorf("SanitizeUsername(%q) = %q contains invalid character %q", tt.username, result, r)
				}
			}

			// Property 5: Start/end constraints (covered by DNS-1123 validation above)
		})
	}
}

// TestKubernetesDNS1123Validation demonstrates that we're using the official Kubernetes validation
func TestKubernetesDNS1123Validation(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		isValid bool
	}{
		{"valid simple", "abc", true},
		{"valid with numbers", "abc123", true},
		{"valid with hyphen", "abc-123", true},
		{"invalid starts with hyphen", "-abc", false},
		{"invalid ends with hyphen", "abc-", false},
		{"invalid uppercase", "ABC", false},
		{"too long", strings.Repeat("a", 64), false},
		{"max length", strings.Repeat("a", 63), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validation.IsDNS1123Label(tt.label)
			isValid := len(errs) == 0
			if isValid != tt.isValid {
				t.Errorf("validation.IsDNS1123Label(%q) valid=%v, want %v (errors: %v)", tt.label, isValid, tt.isValid, errs)
			}
		})
	}
}
