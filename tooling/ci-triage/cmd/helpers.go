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

package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sinceRE = regexp.MustCompile(`^(\d+)([dhw])$`)

// parseSinceDuration converts relative shorthand (7d, 24h, 2w) to a time.Duration.
func parseSinceDuration(value string) (time.Duration, error) {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" {
		return 7 * 24 * time.Hour, nil // default 7 days
	}
	m := sinceRE.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("invalid --since format: %q (use relative like 7d, 24h, 2w)", value)
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid --since unit: %q", m[2])
}
