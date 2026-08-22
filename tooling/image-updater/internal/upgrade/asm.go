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

package upgrade

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ParseAsmRevision splits "asm-<major>-<minor>" into its numeric parts, so
// asm-1-9 compares older than asm-1-10 rather than sorting lexicographically.
func ParseAsmRevision(rev string) (major, minor int, err error) {
	const prefix = "asm-"
	if !strings.HasPrefix(rev, prefix) {
		return 0, 0, fmt.Errorf("asm revision %q missing %q prefix", rev, prefix)
	}
	parts := strings.SplitN(strings.TrimPrefix(rev, prefix), "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("asm revision %q does not match asm-<major>-<minor>", rev)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("asm revision %q: invalid major version: %w", rev, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("asm revision %q: invalid minor version: %w", rev, err)
	}
	return major, minor, nil
}

// CompareAsmRevisions follows the cmp.Ordered contract for use with
// slices.SortFunc. Unparseable revisions sort before valid ones so garbage
// cannot win as the max in HighestAsmRevision.
func CompareAsmRevisions(a, b string) int {
	aM, am, aErr := ParseAsmRevision(a)
	bM, bm, bErr := ParseAsmRevision(b)
	switch {
	case aErr != nil && bErr != nil:
		return strings.Compare(a, b)
	case aErr != nil:
		return -1
	case bErr != nil:
		return +1
	case aM != bM:
		if aM < bM {
			return -1
		}
		return +1
	case am != bm:
		if am < bm {
			return -1
		}
		return +1
	default:
		return 0
	}
}

// HighestAsmRevision returns the maximum revision, or an error if empty.
func HighestAsmRevision(revisions []string) (string, error) {
	if len(revisions) == 0 {
		return "", fmt.Errorf("no asm revisions to pick from")
	}
	sorted := slices.Clone(revisions)
	slices.SortFunc(sorted, CompareAsmRevisions)
	return sorted[len(sorted)-1], nil
}

// IntersectAsmRevisions returns revisions present in every input set. Order
// follows the first set. An empty result means no revision is available
// everywhere; callers should treat that as an error.
func IntersectAsmRevisions(sets ...[]string) []string {
	if len(sets) == 0 {
		return nil
	}
	counts := make(map[string]int, len(sets[0]))
	for _, r := range sets[0] {
		counts[r] = 1
	}
	for _, s := range sets[1:] {
		seen := make(map[string]struct{}, len(s))
		for _, r := range s {
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			if _, ok := counts[r]; ok {
				counts[r]++
			}
		}
	}
	var out []string
	seen := make(map[string]struct{}, len(sets[0]))
	for _, r := range sets[0] {
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		if counts[r] == len(sets) {
			out = append(out, r)
		}
	}
	return out
}
