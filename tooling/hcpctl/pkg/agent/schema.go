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

package agent

import (
	"encoding/json"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
)

// DraftChain is the structured output the agent must produce as its final message.
// This is the pre-hydration format — KQL queries have no share URIs or result tables yet.
type DraftChain struct {
	RootCause   string          `json:"root_cause"`
	Summary     string          `json:"summary"`
	Notes       string          `json:"notes,omitempty"`
	Discovery   []DiscoveryItem `json:"discovery,omitempty"`
	Chain       []ChainLink     `json:"chain"`
	Suggestions []string        `json:"suggestions,omitempty"`
}

// DiscoveryItem is an agent-authored KQL query with a label, used to establish
// provenance for constants in proof queries that are not already covered by the
// pre-gathered data directory (which is embedded automatically during hydration).
type DiscoveryItem struct {
	// Label is a human-readable description of what this discovery item establishes.
	Label string `json:"label"`

	// KQL is an agent-authored Kusto query whose results establish provenance
	// for constants used in proof queries.
	KQL string `json:"kql"`
}

// ChainLink is one link in the causal why-chain. Each link poses a "why?"
// question and provides an answer backed by proof. The first link's question
// is always "Why did this test fail?"; subsequent questions follow naturally
// from the previous answer.
type ChainLink struct {
	Question string      `json:"question"`
	Answer   string      `json:"answer"`
	Notes    string      `json:"notes,omitempty"`
	Proof    []ProofItem `json:"proof"`
}

// ProofItem is evidence supporting a chain link claim.
type ProofItem struct {
	Type string `json:"type"` // "kusto", "code", "log"

	// Kusto proof fields
	KQL  string `json:"kql,omitempty"`
	Note string `json:"note,omitempty"`

	// Code proof fields
	Repo  string `json:"repo,omitempty"`
	File  string `json:"file,omitempty"`
	Lines [2]int `json:"lines,omitempty"` // 1-indexed, inclusive; used by code and log proofs

	// Log proof fields
	Source string `json:"source,omitempty"` // "error" or "output"
}

// HydratedChain extends DraftChain with query results and share URIs.
type HydratedChain struct {
	RootCause   string              `json:"root_cause"`
	Summary     string              `json:"summary"`
	Notes       string              `json:"notes,omitempty"`
	Discovery   []HydratedDiscovery `json:"discovery,omitempty"`
	Chain       []HydratedLink      `json:"chain"`
	Suggestions []string            `json:"suggestions,omitempty"`
}

// HydratedDiscovery is a single leaf query directory from a discovery path,
// hydrated with its README summary, KQL, share URI, and parsed query results.
type HydratedDiscovery struct {
	Directory string         `json:"directory,omitempty"`
	Label     string         `json:"label"`
	KQL       string         `json:"kql"`
	ShareURI  string         `json:"share_uri"`
	Table     *tabular.Table `json:"table,omitempty"`
}

// HydratedLink is a chain link with hydrated proof items.
type HydratedLink struct {
	Question string              `json:"question"`
	Answer   string              `json:"answer"`
	Notes    string              `json:"notes,omitempty"`
	Proof    []HydratedProofItem `json:"proof"`
}

// HydratedProofItem extends ProofItem with fields populated during hydration.
type HydratedProofItem struct {
	ProofItem

	// Kusto proof fields populated during hydration
	ShareURI string         `json:"share_uri,omitempty"`
	Table    *tabular.Table `json:"table,omitempty"`

	// Code proof field populated during hydration by reading the worktree
	CodeExcerpt string `json:"code_excerpt,omitempty"`

	// Log proof field populated during hydration by extracting lines from test logs
	LogExcerpt string `json:"log_excerpt,omitempty"`
}

// ParseDraftChain parses the agent's final output as a DraftChain.
func ParseDraftChain(output string) (*DraftChain, error) {
	// Try to find JSON in the output — the agent may wrap it in markdown code fences.
	jsonStr := extractJSON(output)

	var chain DraftChain
	if err := json.Unmarshal([]byte(jsonStr), &chain); err != nil {
		return nil, err
	}
	return &chain, nil
}

// extractJSON attempts to extract a JSON object from text that may include
// markdown code fences or surrounding prose.
func extractJSON(s string) string {
	// Look for ```json ... ``` fencing first.
	start := -1
	for i := 0; i < len(s)-3; i++ {
		if s[i:i+3] == "```" {
			// Skip the language tag line.
			for j := i + 3; j < len(s); j++ {
				if s[j] == '\n' {
					start = j + 1
					break
				}
			}
			break
		}
	}

	if start >= 0 {
		// Find closing ```.
		for i := start; i < len(s)-3; i++ {
			if s[i:i+3] == "```" {
				return s[start:i]
			}
		}
	}

	// Otherwise, find the first { and last }.
	first := -1
	last := -1
	for i, c := range s {
		if c == '{' && first == -1 {
			first = i
		}
		if c == '}' {
			last = i
		}
	}
	if first >= 0 && last > first {
		return s[first : last+1]
	}
	return s
}
