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

package prow

import (
	"regexp"
	"sort"
)

// dedupPattern pairs a compiled regex with its replacement string.
type dedupPattern struct {
	re          *regexp.Regexp
	replacement string
}

// Patterns for ephemeral identifiers that vary across runs
// but don't change the semantic meaning of an error message.
var dedupPatterns = []dedupPattern{
	// UUIDs: 8-4-4-4-12 hex
	{regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), "<uuid>"},
	// Azure resource groups (rg-xxx-yyy-randomsuffix)
	{regexp.MustCompile(`rg-[a-z0-9-]+`), "<rg>"},
	// Prow-style random suffixes on resource names
	{regexp.MustCompile(`(?:-)([a-z0-9]{5,8})(?:[\s/\]\)"'\\,;:.]|$)`), "-<id>"},
	// Hex addresses
	{regexp.MustCompile(`0x[0-9a-f]{8,}`), "0x..."},
	// Go source file:line references
	{regexp.MustCompile(`(?:\.go:)\d+`), ".go:<line>"},
	// Timestamps in various formats
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<timestamp>"},
	// Numeric IDs (build IDs, port numbers >1024, etc.)
	{regexp.MustCompile(`(?:[^a-zA-Z])\d{5,}(?:[^a-zA-Z])`), "<num>"},
}

// NormalizeForDedup normalizes a message for deduplication by replacing
// ephemeral identifiers with placeholders. The original message is
// preserved verbatim for display.
func NormalizeForDedup(msg string) string {
	normalized := msg
	for _, p := range dedupPatterns {
		normalized = p.re.ReplaceAllString(normalized, p.replacement)
	}
	return normalized
}

// DedupedMessage holds a representative verbatim message and its occurrence count.
type DedupedMessage struct {
	Msg   string `json:"msg"`
	Count int    `json:"count"`
}

// DedupMessages deduplicates messages by structural similarity.
// Returns a slice sorted by count descending.
func DedupMessages(messages []string) []DedupedMessage {
	if len(messages) == 0 {
		return nil
	}

	type entry struct {
		verbatim string
		count    int
	}
	seen := make(map[string]*entry)
	order := make([]string, 0) // preserve insertion order for stable output

	for _, msg := range messages {
		key := NormalizeForDedup(msg)
		if e, ok := seen[key]; ok {
			e.count++
		} else {
			seen[key] = &entry{verbatim: msg, count: 1}
			order = append(order, key)
		}
	}

	result := make([]DedupedMessage, 0, len(seen))
	for _, key := range order {
		e := seen[key]
		result = append(result, DedupedMessage{
			Msg:   e.verbatim,
			Count: e.count,
		})
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	return result
}
