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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// SanitizeUsername converts a username to a format suitable for Kubernetes resource names.
// It follows DNS-1123 label requirements.
func SanitizeUsername(input string) (string, error) {
	// Convert to lowercase
	s := strings.ToLower(input)

	// Replace invalid characters with '-'
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}

	// Trim leading and trailing dashes
	sanitized := strings.Trim(b.String(), "-")

	// Truncate to max 63 characters
	const maxLen = 63
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen]
		sanitized = strings.TrimRight(sanitized, "-")
	}

	// Validate result
	if errs := validation.IsDNS1123Label(sanitized); len(errs) > 0 {
		return "", fmt.Errorf("result '%s' is not a valid DNS-1123 label: %v", sanitized, errs)
	}

	return sanitized, nil
}
