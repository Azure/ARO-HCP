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
	"fmt"
	"strings"
)

// RenderMarkdown produces a low-fidelity markdown document from a hydrated
// analysis chain. This is used to show the agent the full rendered output —
// including query result tables — so it can review narrative coherence,
// evidence quality, and depth before finalizing.
func RenderMarkdown(chain *HydratedChain, testName string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Test Failure Analysis: %s\n\n", testName))

	// Root cause.
	sb.WriteString("## Root Cause\n\n")
	sb.WriteString(chain.RootCause)
	sb.WriteString("\n\n")

	// Summary.
	sb.WriteString("## Summary\n\n")
	sb.WriteString(chain.Summary)
	sb.WriteString("\n\n")
	if chain.Notes != "" {
		sb.WriteString(chain.Notes)
		sb.WriteString("\n\n")
	}

	// Causal chain.
	if len(chain.Chain) > 0 {
		sb.WriteString("## Causal Chain\n\n")
		for i, link := range chain.Chain {
			sb.WriteString(fmt.Sprintf("### %d. Q: %s\n\n", i+1, link.Question))
			sb.WriteString(fmt.Sprintf("**A:** %s\n\n", link.Answer))

			if link.Notes != "" {
				sb.WriteString(link.Notes)
				sb.WriteString("\n\n")
			}

			for j, proof := range link.Proof {
				switch proof.Type {
				case "kusto":
					sb.WriteString(fmt.Sprintf("#### Proof %d (kusto)\n\n", j+1))
					if proof.Note != "" {
						sb.WriteString(proof.Note)
						sb.WriteString("\n\n")
					}
					sb.WriteString("```kql\n")
					sb.WriteString(proof.KQL)
					sb.WriteString("\n```\n\n")
					sb.WriteString(TableToMarkdown(proof.Table))
					sb.WriteString("\n\n")
				case "code":
					sb.WriteString(fmt.Sprintf("#### Proof %d (code)\n\n", j+1))
					if proof.Note != "" {
						sb.WriteString(proof.Note)
						sb.WriteString("\n\n")
					}
					sb.WriteString(fmt.Sprintf("`%s` — `%s` lines %d–%d:\n\n", proof.Repo, proof.File, proof.Lines[0], proof.Lines[1]))
					sb.WriteString("```\n")
					sb.WriteString(proof.CodeExcerpt)
					sb.WriteString("\n```\n\n")
				case "log":
					if proof.Source == "node_console_log" {
						sb.WriteString(fmt.Sprintf("#### Proof %d (log — node console: %s)\n\n", j+1, proof.File))
						if proof.Note != "" {
							sb.WriteString(proof.Note)
							sb.WriteString("\n\n")
						}
						if proof.ArtifactURL != "" {
							sb.WriteString(fmt.Sprintf("Node console log `%s`, lines %d\u2013%d ([download](%s)):\n\n", proof.File, proof.Lines[0], proof.Lines[1], proof.ArtifactURL))
						} else {
							sb.WriteString(fmt.Sprintf("Node console log `%s`, lines %d\u2013%d:\n\n", proof.File, proof.Lines[0], proof.Lines[1]))
						}
					} else {
						sb.WriteString(fmt.Sprintf("#### Proof %d (log \u2014 %s)\n\n", j+1, proof.Source))
						if proof.Note != "" {
							sb.WriteString(proof.Note)
							sb.WriteString("\n\n")
						}
						sb.WriteString(fmt.Sprintf("Test %s log, lines %d\u2013%d:\n\n", proof.Source, proof.Lines[0], proof.Lines[1]))
					}
					sb.WriteString("```\n")
					sb.WriteString(proof.LogExcerpt)
					sb.WriteString("\n```\n\n")
				}
			}
		}
	}

	// Suggestions.
	if len(chain.Suggestions) > 0 {
		sb.WriteString("## Suggestions\n\n")
		for _, s := range chain.Suggestions {
			sb.WriteString(fmt.Sprintf("- %s\n", s))
		}
		sb.WriteString("\n")
	}

	// Facts (appendix).
	if len(chain.Discovery) > 0 {
		sb.WriteString("## Appendix: Discovered Facts\n\n")
		for _, d := range chain.Discovery {
			sb.WriteString(fmt.Sprintf("### %s\n\n", d.Label))
			sb.WriteString("```kql\n")
			sb.WriteString(d.KQL)
			sb.WriteString("\n```\n\n")
			sb.WriteString(TableToMarkdown(d.Table))
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}
