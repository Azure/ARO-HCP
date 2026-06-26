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
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

//go:embed prompts/*
var promptFS embed.FS

// identityPrompt is the shared identity section text used by all providers.
const identityPrompt = "You are a senior SRE specializing in Azure Red Hat OpenShift (ARO-HCP). " +
	"Your task is to perform root-cause analysis on failed e2e tests by examining " +
	"diagnostic data, querying Azure Data Explorer (Kusto), and reading source code."

// tonePrompt is the shared tone section text used by all providers.
const tonePrompt = "Be precise, evidence-driven, and thorough. Every claim must be backed by " +
	"data from tool calls. Prefer structured output over prose. When uncertain, " +
	"investigate further rather than speculate."

// buildDomainContent reads the embedded system prompt, reference documents, and
// exemplar analyses, returning them as a single string. This content is shared
// by all providers.
func buildDomainContent() (string, error) {
	base, err := promptFS.ReadFile("prompts/system.md")
	if err != nil {
		return "", fmt.Errorf("failed to read system prompt: %w", err)
	}

	var sb strings.Builder
	sb.Write(base)

	// Append reference documents.
	refs, err := readDir("prompts/references")
	if err != nil {
		return "", fmt.Errorf("failed to read reference documents: %w", err)
	}
	if len(refs) > 0 {
		sb.WriteString("\n\n## Reference Material\n\n")
		for _, content := range refs {
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}

	// Append exemplars.
	exemplars, err := readDir("prompts/exemplars")
	if err != nil {
		return "", fmt.Errorf("failed to read exemplar documents: %w", err)
	}
	if len(exemplars) > 0 {
		sb.WriteString("\n\n## Exemplar Analyses\n\n")
		sb.WriteString("The following are completed analyses demonstrating the expected quality, depth, and reasoning patterns.\n\n")
		for _, content := range exemplars {
			sb.WriteString(content)
			sb.WriteString("\n\n---\n\n")
		}
	}

	return sb.String(), nil
}

// BuildSystemPrompt returns the complete system prompt as a plain string,
// suitable for providers that accept a single system prompt (e.g. Claude).
// It combines the identity, tone, and domain-specific content (system.md,
// references, exemplars) into one string.
func BuildSystemPrompt() (string, error) {
	domain, err := buildDomainContent()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(identityPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(tonePrompt)
	sb.WriteString("\n\n")
	sb.WriteString(domain)
	return sb.String(), nil
}

// BuildSystemMessageConfig assembles a Copilot-specific SystemMessageConfig
// in "customize" mode. It uses the same shared content as BuildSystemPrompt
// but packages it into Copilot SDK section overrides.
//
// Section strategy (from design review):
//   - SectionIdentity: replace with our domain identity
//   - SectionTone: replace with our analysis tone
//   - SectionCodeChangeRules: remove (agent doesn't write code)
//   - SectionToolInstructions: keep (SDK manages tool descriptions)
//   - SectionToolEfficiency: keep
//   - SectionSafety: keep
//   - SectionCustomInstructions: append domain content (references, exemplars, output schema)
func BuildSystemMessageConfig() (*copilot.SystemMessageConfig, error) {
	domain, err := buildDomainContent()
	if err != nil {
		return nil, err
	}

	return &copilot.SystemMessageConfig{
		Mode: "customize",
		Sections: map[string]copilot.SectionOverride{
			copilot.SectionIdentity: {
				Action:  copilot.SectionActionReplace,
				Content: identityPrompt,
			},
			copilot.SectionTone: {
				Action:  copilot.SectionActionReplace,
				Content: tonePrompt,
			},
			copilot.SectionCodeChangeRules: {
				Action: copilot.SectionActionRemove,
			},
			copilot.SectionCustomInstructions: {
				Action:  copilot.SectionActionAppend,
				Content: domain,
			},
		},
	}, nil
}

// BuildInitialPrompt creates the initial user prompt for an analysis run,
// including the manifest, test logs, and available source code worktrees.
func BuildInitialPrompt(manifest, testError, testOutput, siblingTests, dataDir string, worktreePaths map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`I need you to analyze a failed e2e test. Here is the gathered diagnostic data:

## manifest.json

`)
	sb.WriteString(manifest)
	sb.WriteString("\n\n## Test Error Log\n\n")
	sb.WriteString(testError)
	sb.WriteString("\n\n## Test Output Log\n\n")
	sb.WriteString(testOutput)

	if siblingTests != "" {
		sb.WriteString("\n\n## Sibling Tests\n\n")
		sb.WriteString("The following JSON lists all e2e tests (passing, failing, and skipped) from the same Prow job run. ")
		sb.WriteString("Each entry includes the test name, result, resource group, and time window. ")
		sb.WriteString("You can use the resource group and time window of a **passing** sibling test to query Kusto for known-good baseline logs, ")
		sb.WriteString("then compare them against the failing test's logs to isolate what is different.\n\n")
		sb.WriteString(siblingTests)
	}

	sb.WriteString("\n\n## Data Directory\n\n")
	sb.WriteString(fmt.Sprintf("Pre-gathered diagnostic artifacts (traces, state files, logs) are available at: `%s`\n", dataDir))
	sb.WriteString("Use the available tools to explore this directory and read files as needed.\n")
	sb.WriteString("Reference findings from these data by re-evaluating the Kusto queries, not by referring to these files on disk.\n")

	if len(worktreePaths) > 0 {
		sb.WriteString("\n## Source Code Repositories\n\n")
		sb.WriteString("The following repositories are checked out at the commits that were deployed when this test ran. ")
		sb.WriteString("Use the available tools to read files from these directories when you need to understand the source code.\n\n")
		for repo, path := range worktreePaths {
			sb.WriteString(fmt.Sprintf("- **%s**: `%s`\n", repo, path))
		}
	}

	sb.WriteString("\n\nPlease begin your analysis. Start by reading the manifest to understand what resources were involved, then examine the relevant trace data and state files. Use the available tools to explore the data directory and the kusto_query tool if you need additional data not present in the pre-gathered artifacts.")
	sb.WriteString("\n\nWhen you are done, output your analysis as a JSON object conforming to the draft chain schema.")

	return sb.String()
}

// readDir reads all .md files from a directory in the embedded filesystem,
// returning their contents sorted by filename for deterministic ordering.
func readDir(dir string) ([]string, error) {
	entries, err := fs.ReadDir(promptFS, dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var contents []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := promptFS.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", e.Name(), err)
		}
		contents = append(contents, string(data))
	}
	return contents, nil
}
