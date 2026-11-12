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

// verify-supply-chain fails if a file tracked by git is agent or editor
// configuration that has not been reviewed. Exit code is 1 for a violation,
// 2 on an internal error.
//
// Default-deny, decided entirely from the git index: paths, modes and object
// IDs. No blob is read and nothing is parsed.
//
// Threat model, per-rule reasoning and known gaps:
// docs/ci/supply-chain-presubmit.md.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
)

// Matched as whole path segments at any depth.
var configDirs = []string{".claude", ".copilot", ".cursor", ".vscode"}

// Matched as path prefixes: "agents" or "skills" as a bare segment would match
// any directory of that name. Only .github's agent subtrees, not the CI config.
var configPrefixes = []string{
	".github/agents/",
	".github/skills/",
	".github/instructions/",
}

// allowedConfigFiles maps an approved path to the object ID of the approved
// content, so editing an approved file revokes its approval. Adding or
// changing an entry is a change to a security control: read the file in full
// first, including when only the hash moves. Regenerate with `git hash-object`.
var allowedConfigFiles = map[string]string{
	".claude/OWNERS": "1339739f4fe68150e4a8c071fee63ca4d745d81f",
	".claude/skills/bootstrap-api-version/SKILL.md": "36a339ddf8bb019b0bec79d8ef39cf3c346c02f2",
	".claude/skills/pr-standards/SKILL.md":          "97f14753013baad26a662ba15c020c19a5485350",
	".github/agents/agentic-workflows.agent.md":     "3fee8b7060d3c5307731a3e921f57bf406e91e85",
	".github/instructions/e2e.instructions.md":      "cd16a25d1459cda97bc448652f2212714bd57e90",
	".github/skills/agentic-workflows/SKILL.md":     "37dd349a7c61221e2ceef008624592efbae9dbb5",
}

const workspaceConfigExt = ".code-workspace"

// By basename at any path: the project-scoped form sits at the repository
// root, outside every configuration location.
var mcpConfigFiles = map[string]bool{
	"mcp.json":  true,
	".mcp.json": true,
}

const (
	ruleUnlistedConfig = "unlisted-config"
	ruleModifiedConfig = "modified-config"
	ruleAgentSettings  = "agent-settings"
	ruleEditorConfig   = "editor-config"
	ruleConfigNotFile  = "config-not-a-file"
)

const (
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
)

type finding struct {
	path   string
	rule   string
	detail string
}

// trackedFile is one entry of the git index. The object ID is compared against
// the allowlist, never resolved.
type trackedFile struct {
	path string
	mode string
	oid  string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	findings, err := scanRepository(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if len(findings) == 0 {
		return
	}

	report(os.Stderr, findings)
	os.Exit(1)
}

func scanRepository(root string) ([]finding, error) {
	files, err := trackedFiles(root)
	if err != nil {
		return nil, fmt.Errorf("error listing tracked files in %s: %w", root, err)
	}
	return checkPaths(files), nil
}

// trackedFiles returns every index entry. Untracked files are excluded; a
// force-added one stays tracked and still appears.
func trackedFiles(root string) ([]trackedFile, error) {
	cmd := exec.Command("git", "ls-files", "--stage", "-z")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseIndexEntries(out)
}

// parseIndexEntries reads `git ls-files --stage -z`: "<mode> <sha> <stage>\t<path>"
// per NUL-terminated record. The mode comes from the index rather than the
// working tree, so a fresh clone and a bit-less filesystem agree.
func parseIndexEntries(out []byte) ([]trackedFile, error) {
	var files []trackedFile
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-files entry %q", entry)
		}
		mode, rest, ok := bytes.Cut(meta, []byte{' '})
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-files metadata %q", meta)
		}
		oid, _, ok := bytes.Cut(rest, []byte{' '})
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-files metadata %q", meta)
		}
		files = append(files, trackedFile{path: string(p), mode: string(mode), oid: string(oid)})
	}
	return files, nil
}

// isRegularFile reports whether an entry is a file this repository carries: a
// symlink's blob is a target path, a submodule's a commit we need not have.
func isRegularFile(f trackedFile) bool {
	return f.mode == modeRegular || f.mode == modeExecutable
}

// checkPaths applies every rule. Case order is load-bearing: the two
// location-independent rules run first so they keep their wording, then
// aliases before the allowlist so an approved path cannot launder a symlink.
func checkPaths(files []trackedFile) []finding {
	var findings []finding
	for _, f := range files {
		p := f.path
		lower := strings.ToLower(p)
		inConfigLocation := hasAnySegment(lower, configDirs...) || hasAnyPrefix(lower, configPrefixes...)

		switch {
		case mcpConfigFiles[path.Base(lower)]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleAgentSettings,
				detail: "MCP server configuration must not be committed",
			})
		case path.Ext(lower) == workspaceConfigExt:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleEditorConfig,
				detail: "editor workspace files can run tasks on folder open and must not be committed",
			})
		case !isRegularFile(f) && (inConfigLocation || aliasesAConfigLocation(lower)):
			findings = append(findings, finding{
				path:   p,
				rule:   ruleConfigNotFile,
				detail: fmt.Sprintf("agent and editor configuration paths must be real files; git records mode %s, which names content this repository does not carry and is not resolved here", f.mode),
			})

		// The location match is case-insensitive so .Claude/ cannot evade it;
		// the allowlist lookup is case-sensitive so a near-miss spelling
		// cannot inherit an approved path's approval.
		case inConfigLocation:
			approved, listed := allowedConfigFiles[p]
			switch {
			case !listed:
				findings = append(findings, finding{
					path:   p,
					rule:   ruleUnlistedConfig,
					detail: "not on the reviewed allowlist in hack/verify-supply-chain/main.go",
				})
			case approved != f.oid:
				findings = append(findings, finding{
					path:   p,
					rule:   ruleModifiedConfig,
					detail: fmt.Sprintf("approved as %s but the index records %s", approved, f.oid),
				})
			}
		}
	}
	return findings
}

// aliasesAConfigLocation reports whether an alias at this path would expose a
// configuration location beneath it. A segment match already covers its own
// directory, but a prefix does not: ".github/agents/" never matches
// ".github/agents", so without this a symlink there would hide a whole
// subtree that git records nothing under. Ancestors count for the same
// reason, which is bounded here only because the prefixes are root-anchored.
func aliasesAConfigLocation(lower string) bool {
	for _, prefix := range configPrefixes {
		if strings.HasPrefix(prefix, lower+"/") {
			return true
		}
	}
	return false
}

// hasAnyPrefix takes a lowercased path; every prefix ends in "/" so that
// ".github/agents/" does not match ".github/agentsfoo/".
func hasAnyPrefix(p string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// hasAnySegment matches a whole path element, so ".claude" matches
// "frontend/.claude/x" but not "notclaude/x" or "x/.claude.bak".
func hasAnySegment(p string, segments ...string) bool {
	for _, part := range strings.Split(p, "/") {
		if slices.Contains(segments, part) {
			return true
		}
	}
	return false
}

// remediation differs by rule: an alias is usually legitimate content in the
// wrong shape, and an unlisted file may belong here once somebody has read it.
var remediation = map[string]string{
	ruleModifiedConfig: "An approved file has changed, so its approval no longer applies. Read the diff in full before anything else — appending an instruction to a SKILL.md an agent already trusts needs no new file. If the change is legitimate, update the pinned hash in allowedConfigFiles with `git hash-object <path>` in the same PR.",
	ruleUnlistedConfig: "If the file does not belong in the repository, remove it — agent and editor configuration belongs in your local working copy. If it genuinely does belong, say so in the PR: confirm it is needed, confirm it cannot execute anything by itself, have a second person read it in full, and add its exact path to allowedConfigFiles in hack/verify-supply-chain/main.go in the same PR. That edit is a change to a security control and will be reviewed as one.",
	ruleAgentSettings:  "Remove these from the commit. They belong in your local working copy only.",
	ruleEditorConfig:   "Remove these from the commit. They belong in your local working copy only.",
	ruleConfigNotFile:  "Replace the alias with a real file, or move the content out of the configuration path. Do not resolve it to find out what it points at.",
}

// report prints the findings and the remediation for each rule that appeared.
// Paths are quoted: git permits newlines and escapes in one, so an unquoted
// path lets the reported-on file forge or erase output.
func report(w io.Writer, findings []finding) {
	fmt.Fprintln(w, "ERROR: unreviewed agent or editor configuration found in tracked files.")
	fmt.Fprintln(w)

	var rules []string
	for _, f := range findings {
		fmt.Fprintf(w, "  %q (%s): %s\n", f.path, f.rule, f.detail)
		if !slices.Contains(rules, f.rule) {
			rules = append(rules, f.rule)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "What to do:")
	for _, rule := range rules {
		advice, ok := remediation[rule]
		if !ok {
			// Named rather than blank, so a rule added without advice is
			// visible instead of reading as "nothing to do here".
			advice = "No remediation recorded for this rule; see docs/ci/supply-chain-presubmit.md."
		}
		fmt.Fprintf(w, "  %s: %s\n", rule, advice)
	}

	// Unconditional, and hedged: the check does not read these files, so it
	// cannot say which one would run anything — only that none should be
	// opened to find out. Cloning is inert; an agent or editor loading the
	// repository is what runs configuration.
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If one of these is not yours, do not open or run it. Agent and editor")
	fmt.Fprintln(w, "configuration can execute when an agent or editor loads the repository,")
	fmt.Fprintln(w, "which is how a known supply-chain attack worked. Report it to the")
	fmt.Fprintln(w, "security team rather than investigating it yourself.")

	fmt.Fprintln(w)
	fmt.Fprintln(w, `See the "Security self-check" bullet in CONTRIBUTING.md, and`)
	fmt.Fprintln(w, "docs/ci/supply-chain-presubmit.md for why each rule exists.")
}
