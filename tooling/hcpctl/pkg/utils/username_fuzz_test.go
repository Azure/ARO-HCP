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

//go:build go1.18
// +build go1.18

package utils

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func FuzzSanitizeUsername(f *testing.F) {
	// Add seed corpus with interesting cases
	testcases := []string{
		"",
		"a",
		"ab",
		"abc",
		"john",
		"john@example.com",
		"JOHN.DOE@MICROSOFT.COM",
		"domain\\username",
		"user_name",
		"user-name",
		"123",
		"user123",
		"---",
		"@@@",
		"user@@@",
		"@@@user",
		"user@@@user",
		strings.Repeat("a", 63),
		strings.Repeat("a", 64),
		strings.Repeat("a", 100),
		strings.Repeat("-", 63),
		"a" + strings.Repeat("-", 61) + "b",
		"user名前",
		"🎉emoji🎉",
		"\x00\x01\x02",
		"user\nname",
		"user\tname",
		"user name",
		"user  name",
		"user...name",
		"user___name",
		"user@#$%name",
		"!@#$%^&*()",
		"<script>alert('xss')</script>",
		"'; DROP TABLE users; --",
	}

	for _, tc := range testcases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, username string) {

		// Test that the function doesn't panic
		result, err := SanitizeUsername(username)

		// If no error, verify the result is a valid DNS-1123 label
		if err == nil {
			// Check the result is valid using official Kubernetes validation
			if errs := validation.IsDNS1123Label(result); len(errs) > 0 {
				t.Errorf("SanitizeUsername(%q) = %q, which is not a valid DNS-1123 label: %v", username, result, errs)
			}

			// Property: result should be deterministic
			result2, err2 := SanitizeUsername(username)
			if err2 != nil || result != result2 {
				t.Errorf("SanitizeUsername(%q) is not deterministic: first=%q, second=%q", username, result, result2)
			}

			// Property: result length should be 1-63
			if len(result) < 1 || len(result) > 63 {
				t.Errorf("SanitizeUsername(%q) = %q, length %d is not in range [1,63]", username, result, len(result))
			}

			// Property: result should only contain valid characters
			for _, r := range result {
				if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
					t.Errorf("SanitizeUsername(%q) = %q contains invalid character %q", username, result, r)
				}
			}

			// Property: start/end alphanumeric (covered by DNS-1123 validation above)
		} else {
			// If error, verify it's for a valid reason
			if username != "" {
				// Try to understand why it failed
				// Convert and check if it would have any valid characters
				hasValidChar := false
				for _, r := range strings.ToLower(username) {
					if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
						hasValidChar = true
						break
					}
				}
				if hasValidChar {
					t.Errorf("SanitizeUsername(%q) returned error %v but input has valid characters", username, err)
				}
			}
		}
	})
}
