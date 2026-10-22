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

//go:embed prompts
var promptFS embed.FS

// Mode selects which framing the analysis prompts use.
type Mode int

const (
	// ModeTest is the failed-e2e-test analysis framing. Its prompts are
	// byte-for-byte identical to the original single-purpose implementation.
	ModeTest Mode = iota
	// ModeIntent is the free-form investigation framing driven by a
	// human-written objective (e.g. a customer complaint about a cluster).
	ModeIntent
	// ModeDynamic is the objective-driven investigation framing used when no
	// snapshot has been gathered: the agent discovers everything itself by
	// querying Kusto, guided by the query library in its workspace.
	ModeDynamic
)

// IdentityPrompt is the shared identity section text used by all providers in
// test mode. Callers pass this to ProviderSessionConfig.IdentityPrompt so
// prompt composition is centralized in the caller rather than scattered across
// providers.
const IdentityPrompt = "You are a senior SRE specializing in Azure Red Hat OpenShift (ARO-HCP). " +
	"Your task is to perform root-cause analysis on failed e2e tests by examining " +
	"diagnostic data, querying Azure Data Explorer (Kusto), and reading source code."

// IntentIdentityPrompt is the identity section text used in intent mode, where
// the objective is a free-form investigation rather than a failed test.
const IntentIdentityPrompt = "You are a senior SRE specializing in Azure Red Hat OpenShift (ARO-HCP). " +
	"Your task is to perform root-cause analysis of operational and customer-reported " +
	"issues by examining diagnostic data, querying Azure Data Explorer (Kusto), and reading source code."

// DynamicIdentityPrompt is the identity section text used in dynamic mode,
// where the investigation starts from an objective with no gathered data.
const DynamicIdentityPrompt = "You are a senior SRE specializing in Azure Red Hat OpenShift (ARO-HCP). " +
	"Your task is to investigate operational questions and customer-reported issues from a written " +
	"objective by querying Azure Data Explorer (Kusto) and reading source code."

// IdentityPromptFor returns the identity section text for the given mode.
func IdentityPromptFor(mode Mode) string {
	switch mode {
	case ModeIntent:
		return IntentIdentityPrompt
	case ModeDynamic:
		return DynamicIdentityPrompt
	default:
		return IdentityPrompt
	}
}

// systemFragment is one ordered section of the domain system prompt. A section
// is either shared across modes (one file) or mode-specific (one
// "<base>.<mode>.md" file per mode). The whole prompt is these fragments
// concatenated in order.
type systemFragment struct {
	// path is set for a section whose text is shared by the snapshot-based
	// modes (test and intent).
	path string
	// dynamicPath, when set alongside path, replaces path in dynamic mode. It
	// is used for shared sections whose text assumes a gathered snapshot or a
	// failed test.
	dynamicPath string
	// variant is the base path for a mode-specific section; the file read is
	// "<variant>.<mode>.md" (mode being "test", "intent", or "dynamic").
	variant string
}

// systemFragments is the ordered list of sections that make up the domain
// system prompt. Every mode assembles it with the exact same flow — they only
// differ in which file each section resolves to. Concatenating the
// test variants reproduces the original single-file prompt byte for byte.
var systemFragments = []systemFragment{
	{path: "prompts/system/00_header.md"},
	{variant: "prompts/system/10_recursive_why"},
	{path: "prompts/system/20_standards.md", dynamicPath: "prompts/system/20_standards.dynamic.md"},
	{path: "prompts/system/30_output_schema.md", dynamicPath: "prompts/system/30_output_schema.dynamic.md"},
	{variant: "prompts/system/40_chain_link_rules"},
	{path: "prompts/system/45_taxonomy_classification.md", dynamicPath: "prompts/system/45_taxonomy_classification.dynamic.md"},
	{path: "prompts/system/50_markdown_formatting.md"},
	{variant: "prompts/system/60_debugging_methodology"},
	{path: "prompts/system/70_available_data_sources.md", dynamicPath: "prompts/system/70_available_data_sources.dynamic.md"},
	{path: "prompts/system/80_discovery.md", dynamicPath: "prompts/system/80_discovery.dynamic.md"},
	{path: "prompts/system/90_kql_quality.md", dynamicPath: "prompts/system/90_kql_quality.dynamic.md"},
	{path: "prompts/system/95_epistemological.md", dynamicPath: "prompts/system/95_epistemological.dynamic.md"},
	{variant: "prompts/system/99_methodology"},
}

// modeSuffix is the filename suffix used to pick a mode-specific fragment.
func (m Mode) modeSuffix() string {
	switch m {
	case ModeIntent:
		return "intent"
	case ModeDynamic:
		return "dynamic"
	default:
		return "test"
	}
}

// fragmentPath returns the embedded file that supplies this section in mode.
func (f systemFragment) fragmentPath(mode Mode) string {
	if f.variant != "" {
		return fmt.Sprintf("%s.%s.md", f.variant, mode.modeSuffix())
	}
	if mode == ModeDynamic && f.dynamicPath != "" {
		return f.dynamicPath
	}
	return f.path
}

// buildSystemBody assembles the ordered system-prompt fragments for a mode.
func buildSystemBody(mode Mode) (string, error) {
	var sb strings.Builder
	for _, f := range systemFragments {
		path := f.fragmentPath(mode)
		data, err := promptFS.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read system prompt fragment %s: %w", path, err)
		}
		sb.Write(data)
	}
	return sb.String(), nil
}

// TonePrompt is the shared tone section text used by all providers.
// Callers pass this to ProviderSessionConfig.TonePrompt so prompt
// composition is centralized in the caller rather than scattered across
// providers.
const TonePrompt = "Be precise, evidence-driven, and thorough. Every claim must be backed by " +
	"data from tool calls. Prefer structured output over prose. When uncertain, " +
	"investigate further rather than speculate."

// buildDomainContent assembles the domain-specific prompt: the mode's system
// prompt (built from ordered section fragments), the shared reference documents,
// and the shared exemplar analyses. The assembly flow is identical for every
// mode; only the per-mode fragments (see systemFragments) differ, and dynamic
// mode omits the exemplars, which walk through gathered snapshots of failed
// tests.
func buildDomainContent(mode Mode) (string, error) {
	systemBody, err := buildSystemBody(mode)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(systemBody)

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

	if mode == ModeDynamic {
		return sb.String(), nil
	}

	// Append exemplars. The same exemplars are used in test and intent modes —
	// they teach the expected quality, depth, and reasoning patterns regardless
	// of framing.
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

// BuildDomainPrompt returns only the domain-specific content (system.md,
// references, exemplars) without the identity or tone preamble. This is
// the preferred function for callers that pass the prompt through the
// provider-neutral ProviderSessionConfig — each provider is responsible
// for incorporating the shared identity and tone sections in its native
// format (e.g. Copilot section overrides, Claude system message prefix).
func BuildDomainPrompt() (string, error) {
	return buildDomainContent(ModeTest)
}

// BuildDomainPromptFor returns the domain-specific content for the given mode.
func BuildDomainPromptFor(mode Mode) (string, error) {
	return buildDomainContent(mode)
}

// BuildSystemPrompt returns the complete system prompt as a plain string,
// suitable for providers that accept a single system prompt (e.g. Claude).
// It combines the identity, tone, and domain-specific content (system.md,
// references, exemplars) into one string.
func BuildSystemPrompt() (string, error) {
	domain, err := buildDomainContent(ModeTest)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(IdentityPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(TonePrompt)
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
	domain, err := buildDomainContent(ModeTest)
	if err != nil {
		return nil, err
	}

	return &copilot.SystemMessageConfig{
		Mode: "customize",
		Sections: map[string]copilot.SectionOverride{
			copilot.SectionIdentity: {
				Action:  copilot.SectionActionReplace,
				Content: IdentityPrompt,
			},
			copilot.SectionTone: {
				Action:  copilot.SectionActionReplace,
				Content: TonePrompt,
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

// BuildIntentInitialPrompt creates the initial user prompt for a free-form
// investigation. The objective is a human-written intent (e.g. a customer
// complaint). Test logs and sibling tests are optional context — they are
// included only when present, and the model is told not to assume a test
// failure occurred.
func BuildIntentInitialPrompt(intent, manifest, testError, testOutput, siblingTests, dataDir string, worktreePaths map[string]string) string {
	var sb strings.Builder
	sb.WriteString("I need you to investigate an issue with an ARO-HCP cluster. ")
	sb.WriteString("The objective below is written by a human and describes the observed symptom or question to answer. ")
	sb.WriteString("Analyze the gathered diagnostic data to determine the root cause.\n\n")
	sb.WriteString("## Investigation Objective\n\n")
	sb.WriteString(intent)
	sb.WriteString("\n\n## manifest.json\n\n")
	sb.WriteString(manifest)

	// Optional context: this snapshot may have come from an e2e test run
	// (from-prow-job) or directly from a resource (from-resource). Include
	// whatever is present, but frame it as supporting context only.
	if testError != "" || testOutput != "" || siblingTests != "" {
		sb.WriteString("\n\n## Additional Context\n\n")
		sb.WriteString("This snapshot may have been gathered from an e2e test run. The following artifacts ")
		sb.WriteString("are provided as supporting context — do not assume the objective is a test failure.\n")
		if testError != "" {
			sb.WriteString("\n### Test Error Log\n\n")
			sb.WriteString(testError)
		}
		if testOutput != "" {
			sb.WriteString("\n\n### Test Output Log\n\n")
			sb.WriteString(testOutput)
		}
		if siblingTests != "" {
			sb.WriteString("\n\n### Sibling Tests\n\n")
			sb.WriteString("The following JSON lists e2e tests from the same Prow job run, each with its ")
			sb.WriteString("resource group and time window. You can query a comparable **passing** run for ")
			sb.WriteString("known-good baseline logs to contrast against the affected cluster.\n\n")
			sb.WriteString(siblingTests)
		}
	}

	sb.WriteString("\n\n## Data Directory\n\n")
	sb.WriteString(fmt.Sprintf("Pre-gathered diagnostic artifacts (traces, state files, logs) are available at: `%s`\n", dataDir))
	sb.WriteString("Use the available tools to explore this directory and read files as needed.\n")
	sb.WriteString("Reference findings from these data by re-evaluating the Kusto queries, not by referring to these files on disk.\n")

	if len(worktreePaths) > 0 {
		sb.WriteString("\n## Source Code Repositories\n\n")
		sb.WriteString("The following repositories are checked out at the commits deployed in the affected environment. ")
		sb.WriteString("Use the available tools to read files from these directories when you need to understand the source code.\n\n")
		for repo, path := range worktreePaths {
			sb.WriteString(fmt.Sprintf("- **%s**: `%s`\n", repo, path))
		}
	}

	sb.WriteString("\n\nPlease begin your investigation. Start by reading the manifest to understand the affected resources, then examine the relevant trace data and state files. Use the available tools to explore the data directory and the kusto_query tool if you need additional data not present in the pre-gathered artifacts.")
	sb.WriteString("\n\nWhen you are done, output your analysis as a JSON object conforming to the draft chain schema.")

	return sb.String()
}

// DynamicContext describes where a dynamic investigation gets its data: the
// Kusto cluster and databases to query, and the query library written to the
// agent's workspace.
type DynamicContext struct {
	// KustoCluster is the URI of the Kusto cluster holding the ARO-HCP logs.
	KustoCluster string
	// ServiceDatabase is the service log database (normally ServiceLogs).
	ServiceDatabase string
	// HCPDatabase is the hosted control plane log database (normally
	// HostedControlPlaneLogs).
	HCPDatabase string
	// MonitoringEventsDatabase is the alert database (normally MonitoringEvents).
	MonitoringEventsDatabase string
	// QueryLibraryDir is the path of the query library in the agent's workspace.
	QueryLibraryDir string
	// QueryResultsDir is the directory where run_query writes query outputs.
	QueryResultsDir string
	// LibraryOverview is the query library's overview (its README.md), which
	// explains parameters and the dependencies between queries.
	LibraryOverview string
}

// BuildDynamicInitialPrompt creates the initial user prompt for a dynamic
// investigation, which starts from the objective alone: no snapshot has been
// gathered, so the agent discovers every fact itself through the query
// library tools, falling back to ad-hoc kusto_query queries.
func BuildDynamicInitialPrompt(objective string, dc DynamicContext, worktreePaths map[string]string) string {
	var sb strings.Builder
	sb.WriteString("I need you to investigate an ARO-HCP issue or question. ")
	sb.WriteString("The objective below is written by a human and describes what to investigate. ")
	sb.WriteString("No diagnostic data has been gathered in advance: find the evidence yourself by querying Kusto ")
	sb.WriteString("and reading source code, then determine the root cause.\n\n")
	sb.WriteString("## Investigation Objective\n\n")
	sb.WriteString(objective)

	sb.WriteString("\n\n## Query Library\n\n")
	sb.WriteString("The query library holds the curated KQL queries that diagnostic snapshots are built from. Use it through these tools:\n\n")
	sb.WriteString("- `list_queries`: find queries (optionally filtered by component or topic), with the parameters each needs and whether it can run now\n")
	sb.WriteString("- `describe_query`: a query's README (what it shows, what to look for, where to go next), its parameters, and the KQL it would run\n")
	sb.WriteString("- `describe_param`: what a parameter is, its current value, and which queries provide and use it\n")
	sb.WriteString("- `run_query`: render and run a query. Parameter values you pass, and values that queries discover, are remembered for later queries, ")
	sb.WriteString("so pass each value once; a missing-parameter error names the query that provides it\n\n")
	if dc.QueryResultsDir != "" {
		sb.WriteString(fmt.Sprintf("`run_query` writes each query's README, KQL and complete results to `%s`; read those files when the returned rows are truncated.\n", dc.QueryResultsDir))
	}
	if dc.QueryLibraryDir != "" {
		sb.WriteString(fmt.Sprintf("The library's files (templates, READMEs, `params.yaml`, `guides/`) are also available at `%s` for searching.\n", dc.QueryLibraryDir))
	}
	if dc.LibraryOverview != "" {
		sb.WriteString("\nThe library's overview follows.\n\n")
		sb.WriteString(demoteHeadings(strings.TrimSpace(dc.LibraryOverview), 2))
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Kusto\n\n")
	sb.WriteString("The library tools target the Kusto cluster and databases below. For questions the library does not cover, ")
	sb.WriteString("run ad-hoc queries with the kusto_query tool, referencing tables with fully qualified ")
	sb.WriteString("`cluster('<cluster>').database('<database>').table('<table>')` expressions using these values:\n\n")
	sb.WriteString(fmt.Sprintf("- **Cluster**: `%s`\n", dc.KustoCluster))
	sb.WriteString(fmt.Sprintf("- **Service logs database**: `%s`\n", dc.ServiceDatabase))
	sb.WriteString(fmt.Sprintf("- **Hosted control plane logs database**: `%s`\n", dc.HCPDatabase))
	sb.WriteString(fmt.Sprintf("- **Monitoring events database**: `%s`\n", dc.MonitoringEventsDatabase))

	if len(worktreePaths) > 0 {
		sb.WriteString("\n## Source Code Repositories\n\n")
		sb.WriteString("The following repositories are checked out locally. ")
		sb.WriteString("Use the available tools to read files from these directories when you need to understand the source code.\n\n")
		repos := make([]string, 0, len(worktreePaths))
		for repo := range worktreePaths {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		for _, repo := range repos {
			sb.WriteString(fmt.Sprintf("- **%s**: `%s`\n", repo, worktreePaths[repo]))
		}
	}

	sb.WriteString("\n\nPlease begin your investigation. Identify the affected resources and time window from the objective, ")
	sb.WriteString("then use list_queries and run_query to discover the identifiers you need and gather evidence.")
	sb.WriteString("\n\nWhen you are done, output your analysis as a JSON object conforming to the draft chain schema.")

	return sb.String()
}

// demoteHeadings shifts every markdown heading in md down by levels, so that
// an embedded document nests under the heading that introduces it.
func demoteHeadings(md string, levels int) string {
	lines := strings.Split(md, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.HasPrefix(line, "#") {
			lines[i] = strings.Repeat("#", levels) + line
		}
	}
	return strings.Join(lines, "\n")
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
