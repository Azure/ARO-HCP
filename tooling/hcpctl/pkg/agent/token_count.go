// Copyright 2026 Microsoft Corporation
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

package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseTokenCount parses a non-negative token count, optionally suffixed with
// k or K (1,000 tokens, not 1,024). Empty and zero mean use the runtime default.
// It is exported so programmatic callers such as the release dashboard can
// share the CLI's parsing without importing the snapshot command package.
func ParseTokenCount(value string) (int, error) {
	digits := strings.TrimSpace(value)
	if digits == "" {
		return 0, nil
	}
	multiplier := 1
	if strings.HasSuffix(digits, "k") || strings.HasSuffix(digits, "K") {
		multiplier = 1000
		digits = digits[:len(digits)-1]
	}
	invalid := func() (int, error) {
		return 0, fmt.Errorf("invalid token count %q: expected a non-negative integer, optionally suffixed with k (e.g. 128000 or 128k)", value)
	}
	if digits == "" {
		return invalid()
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return invalid()
		}
	}
	count, err := strconv.Atoi(digits)
	if err != nil || count > int(^uint(0)>>1)/multiplier {
		return invalid()
	}
	return count * multiplier, nil
}
