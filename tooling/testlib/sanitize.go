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

package testlib

import "strings"

// SanitizeTestName replaces characters that are not alphanumeric, dashes, or
// underscores with underscores, producing a valid filesystem path component.
// Names longer than 200 characters are truncated.
func SanitizeTestName(name string) string {
	var b []byte
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b = append(b, byte(r))
		} else {
			b = append(b, '_')
		}
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

// SanitizeTestNameOld creates a filesystem-safe directory name from a test's
// hierarchy of texts, which were originaly used in E2E test reports.
func SanitizeTestNameOld(parts []string) string {
        name := strings.Join(parts, "_")
        name = strings.Map(func(r rune) rune {
                if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
                        return '_'
                }
                if r == ' ' {
                        return '_'
                }
                return r
        }, name)
        // Truncate to a reasonable length for filesystem paths
        if len(name) > 200 {
                name = name[:200]
        }
        return name
}
