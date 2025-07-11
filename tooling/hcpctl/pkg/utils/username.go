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
)

// SanitizeUsername converts a username to a format suitable for Kubernetes resource names.
// It follows DNS-1123 label requirements.
func SanitizeUsername(username string) (string, error) {
	if username == "" {
		return "", fmt.Errorf("username cannot be empty")
	}

	// Convert to lowercase and replace invalid characters with hyphens
	var result strings.Builder
	for _, r := range strings.ToLower(username) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}

	// Remove leading/trailing hyphens and truncate to 63 chars
	sanitized := strings.Trim(result.String(), "-")
	if len(sanitized) > 63 {
		sanitized = strings.TrimSuffix(sanitized[:63], "-")
	}

	if sanitized == "" {
		return "", fmt.Errorf("username %q contains no valid characters", username)
	}

	return sanitized, nil
}
