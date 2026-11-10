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

// verify-supply-chain fails if any file tracked by git is a committed AI-agent
// settings file, MCP server configuration, or editor configuration. Exit code
// is 1 for a violation, 2 on an internal error.
//
// Every rule is decided from the git index alone: paths and modes, never file
// contents. That is deliberate. These files are refused by name whatever is
// inside them, so reading one could only change the wording of a failure that
// has already happened — and it would mean asserting from a JSON key whether a
// colleague's misplaced config is an attack, which is not a judgement any
// filename check can make.
//
// Threat model, per-rule reasoning, and the scope left to human review:
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

// Matched as whole path segments at any depth: .gitignore anchors /.claude and
// /.vscode at the root, so frontend/.claude/settings.json would otherwise go
// unnoticed, and the other vendors are not ignored anywhere.
//
// This is a short list of agents by deliberate choice, not an attempt at an
// index of them: enumerating vendors is a list that goes stale the week a new
// tool ships while still looking complete. What it covers is the directories a
// contributor to this repository plausibly has configured, which is where an
// accidental commit comes from. Coverage for the rest is the filename rules
// below, which are vendor-neutral, and human review.
var (
	agentConfigDirs  = []string{".claude", ".copilot", ".cursor"}
	editorConfigDirs = []string{".vscode"}
)

// configDirs is every directory the alias rule guards, agent and editor alike.
var configDirs = slices.Concat(agentConfigDirs, editorConfigDirs)

// Matched by extension at any path: a workspace file carries what .vscode/
// does, under a name of the author's choosing and usually at the repo root.
const workspaceConfigExt = ".code-workspace"

// agentSettingsFiles grant an agent standing permission to execute things.
// Matched under any agent directory rather than per vendor: hooks.json is
// Cursor's spelling today, and a name that means "run this" under one agent is
// not something to wait for a second vendor to adopt before listing.
//
// The name is the whole rule. None of these may be committed, empty or
// populated, so what the file contains changes nothing about the outcome.
var agentSettingsFiles = map[string]bool{
	"settings.json":       true,
	"settings.local.json": true,
	"mcp.json":            true,
	"hooks.json":          true,
}

// mcpConfigFiles name an MCP server set, every entry of which carries a command
// the agent launches. By basename at any path: the project-scoped form sits at
// the repository root with no .claude segment to key off.
var mcpConfigFiles = map[string]bool{
	"mcp.json":  true,
	".mcp.json": true,
}

const (
	ruleAgentSettings = "agent-settings"
	ruleEditorConfig  = "editor-config"
	ruleConfigNotFile = "config-not-a-file"
)

// finding is a single violation.
type finding struct {
	path   string
	rule   string
	detail string
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

// scanRepository applies every rule to the git index at root. Separate from
// main so the rules can be tested as they actually run.
func scanRepository(root string) ([]finding, error) {
	files, err := trackedFiles(root)
	if err != nil {
		return nil, fmt.Errorf("error listing tracked files in %s: %w", root, err)
	}
	return checkPaths(files), nil
}

// Index modes. Only a regular file is a file in this repository; a symlink
// names a path elsewhere and a gitlink a commit in another repository.
const (
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
)

// trackedFile is one entry of the git index.
type trackedFile struct {
	path string
	mode string
}

// trackedFiles returns every entry in the git index. Untracked files are
// excluded by design — a gitignored settings.local.json in a working copy is
// not what this check is about — but gitignoring is no escape hatch either: a
// force-added file stays tracked and keeps appearing here.
func trackedFiles(root string) ([]trackedFile, error) {
	// --stage prints "<mode> <sha> <stage>\t<path>", so the entry's type comes
	// from the index rather than the working tree. That keeps the check honest
	// on a fresh clone and on filesystems that do not carry the executable bit.
	// Only the mode and the path are read; the object ID is never resolved.
	cmd := exec.Command("git", "ls-files", "--stage", "-z")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []trackedFile
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-files entry %q", entry)
		}
		mode, _, ok := bytes.Cut(meta, []byte{' '})
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-files metadata %q", meta)
		}
		files = append(files, trackedFile{path: string(p), mode: string(mode)})
	}
	return files, nil
}

// isRegularFile reports whether an index entry is a file this repository
// actually carries. A symlink's blob is a target path rather than the target,
// and a submodule's object is a commit this repository need not have; neither
// is resolved here, so neither counts.
func isRegularFile(f trackedFile) bool {
	return f.mode == modeRegular || f.mode == modeExecutable
}

// checkPaths applies every rule. Pure, so the denylist is testable without a
// repository on disk. Comparisons are against the lowercased path: git's index
// is case-sensitive, so .Claude/settings.json is a distinct tracked path. Case
// order is load-bearing; read the comments before moving.
func checkPaths(files []trackedFile) []finding {
	var findings []finding
	for _, f := range files {
		p := f.path
		lower := strings.ToLower(p)
		base := path.Base(lower)
		switch {
		case mcpConfigFiles[base]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleAgentSettings,
				detail: "MCP server configuration must not be committed",
			})
		case hasAnySegment(lower, agentConfigDirs...) && agentSettingsFiles[base]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleAgentSettings,
				detail: "AI-agent settings files must not be committed",
			})
		// By extension anywhere, because unlike the files above this one has no
		// fixed name and no required directory — it usually sits at the
		// repository root, where the editor directory rule cannot see it.
		case path.Ext(lower) == workspaceConfigExt:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleEditorConfig,
				detail: "editor workspace files can run tasks on folder open and must not be committed",
			})

		// After the named-file rules so they keep their more specific wording,
		// and before the editor rule, which would otherwise swallow an aliased
		// .vscode. An alias records nothing for the paths underneath, so
		// frontend/.claude/settings.json is never a tracked path that any
		// filename rule could match — yet it is what an agent resolves and
		// reads. Keyed on isRegularFile so an unanticipated mode is refused too.
		case !isRegularFile(f) && hasAnySegment(lower, configDirs...):
			findings = append(findings, finding{
				path:   p,
				rule:   ruleConfigNotFile,
				detail: fmt.Sprintf("agent and editor configuration paths must be real files; git records mode %s, which names content this repository does not carry and is not resolved here", f.mode),
			})

		// Last, and deliberately the directory rather than a filename list:
		// enumerating has only to miss once, and the set of files VS Code
		// executes on folder open is Microsoft's to grow. Nothing under
		// .vscode/ is legitimately tracked here.
		case hasAnySegment(lower, editorConfigDirs...):
			findings = append(findings, finding{
				path:   p,
				rule:   ruleEditorConfig,
				detail: "nothing under an editor configuration directory may be committed",
			})
		}
	}
	return findings
}

// hasAnySegment matches a whole path element against any of segments, so
// ".claude" matches "frontend/.claude/x" but not "notclaude/x" or
// "x/.claude.bak". Callers pass a lowercased path.
func hasAnySegment(p string, segments ...string) bool {
	for _, part := range strings.Split(p, "/") {
		if slices.Contains(segments, part) {
			return true
		}
	}
	return false
}

// remediation is what to actually do about each rule, keyed by rule name. The
// advice differs in kind and a single blanket line gets it wrong: a settings
// file is unwanted and should go, whereas an alias is usually a legitimate
// asset in the wrong shape, and telling its author to delete it costs them
// work the check never meant to reject.
var remediation = map[string]string{
	ruleAgentSettings: "Remove these from the commit. They belong in your local working copy only.",
	ruleEditorConfig:  "Remove these from the commit. They belong in your local working copy only.",
	ruleConfigNotFile: "Replace the alias with a real file, or move the content out of the configuration path. Do not resolve it to find out what it points at.",
}

// report prints the findings, then what to do about each rule that appeared.
//
// Paths are quoted because git permits any byte in one except NUL and the
// separator, making an unquoted path a way for the reported-on thing to write
// the report: a newline forges lines, an ANSI sequence erases those above.
// Neither changes the exit code, but a security tool whose output its subject
// can write is not worth reading.
func report(w io.Writer, findings []finding) {
	fmt.Fprintln(w, "ERROR: committed agent or editor configuration found in tracked files.")
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
			// A new rule without advice is a bug, but a blank line here would
			// hide it. Say so rather than defaulting to "delete the file",
			// which is the wrong answer for one of the three rules.
			advice = "No remediation recorded for this rule; see docs/ci/supply-chain-presubmit.md."
		}
		fmt.Fprintf(w, "  %s: %s\n", rule, advice)
	}

	// Printed for every finding rather than decided per file. Agent and editor
	// configuration is executable by design, and this check deliberately does
	// not read it, so it cannot and does not say which of these is dangerous.
	// What it can say is that none of them should be run to find out.
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If one of these is not yours, do not open or run it: this configuration")
	fmt.Fprintln(w, "executes on clone, which is how a known supply-chain attack worked. Report")
	fmt.Fprintln(w, "it to the security team rather than investigating it yourself.")

	fmt.Fprintln(w)
	fmt.Fprintln(w, `See the "Security self-check" bullet in CONTRIBUTING.md, and`)
	fmt.Fprintln(w, "docs/ci/supply-chain-presubmit.md for why each rule exists.")
}
