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

// verify-supply-chain fails if any file tracked by git is committed agent or
// editor configuration that has not been reviewed. Exit code is 1 for a
// violation, 2 on an internal error.
//
// The rule is default-deny: nothing under an agent or editor configuration
// directory may be committed unless its exact path is on the allowlist below.
// A denylist of known-dangerous filenames was tried first and could not hold —
// it blocked .claude/settings.json while letting .claude/skills/x/my-hooks.json
// through with the same payload, because the danger is in what loads the file,
// not in what it is called.
//
// Every rule is decided from the git index: paths and modes, never file
// contents. Nothing is parsed, so nothing has to guess whether a colleague's
// misplaced config is an attack — a question no filename check can answer.
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

// configDirs are matched as whole path segments at any depth: .gitignore
// anchors /.claude and /.vscode at the root, so frontend/.claude/settings.json
// would otherwise go unnoticed, and the other vendors are not ignored anywhere.
//
// This is a short list of agents by deliberate choice, not an attempt at an
// index of them: enumerating vendors is a list that goes stale the week a new
// tool ships while still looking complete. What it covers is the directories a
// contributor to this repository plausibly has configured, which is where an
// accidental commit comes from. Coverage for the rest is the filename rules
// below, which are vendor-neutral, and human review.
var configDirs = []string{".claude", ".copilot", ".cursor", ".vscode"}

// allowedConfigFiles are the only paths under configDirs that may be
// committed. Everything else is refused.
//
// This is an allowlist rather than a denylist of dangerous names because the
// danger is not in the name. An agent loads what its own configuration points
// it at, so a payload simply moves to a filename no denylist anticipated.
// Default-deny removes the guessing: a new file here is refused until somebody
// has read it and said so.
//
// It lives in this source file on purpose. Read from a data file it would be
// configuration the gate obeys, and "add a wildcard to the allowlist" is a far
// quieter diff than "change the verifier" — which is exactly the review this
// list exists to force.
//
// Paths are matched exactly, including case. The directory match below is
// case-insensitive so that .Claude/ cannot slip past it; this one must not be,
// or a near-miss spelling of an approved path would inherit its approval.
//
// Adding an entry is a change to a security control. Before you do: confirm
// the file is genuinely needed, confirm it cannot execute anything by itself,
// and have someone read it in full. A SKILL.md is prose an agent follows — it
// needs no executable bit and no script, and no automated rule here inspects
// it.
var allowedConfigFiles = map[string]bool{
	".claude/skills/bootstrap-api-version/SKILL.md": true,
	".claude/skills/pr-standards/SKILL.md":          true,
}

// Matched by extension at any path: a workspace file carries what .vscode/
// does, under a name of the author's choosing and usually at the repository
// root, which is outside every directory the allowlist governs.
const workspaceConfigExt = ".code-workspace"

// mcpConfigFiles name an MCP server set, every entry of which carries a command
// the agent launches. By basename at any path: the project-scoped form sits at
// the repository root, again outside the configuration directories.
var mcpConfigFiles = map[string]bool{
	"mcp.json":  true,
	".mcp.json": true,
}

const (
	ruleUnlistedConfig = "unlisted-config"
	ruleAgentSettings  = "agent-settings"
	ruleEditorConfig   = "editor-config"
	ruleConfigNotFile  = "config-not-a-file"
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
// force-added file stays tracked and keeps appearing here. Both allowlisted
// files are exactly that, committed under a gitignored /.claude/.
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

// checkPaths applies every rule. Pure, so the rules are testable without a
// repository on disk. Case order is load-bearing; read the comments before
// moving anything.
func checkPaths(files []trackedFile) []finding {
	var findings []finding
	for _, f := range files {
		p := f.path
		lower := strings.ToLower(p)
		base := path.Base(lower)
		inConfigDir := hasAnySegment(lower, configDirs...)

		switch {
		// First, the two rules that reach outside the configuration
		// directories, so they keep their more specific wording when a file
		// sits inside one as well.
		case mcpConfigFiles[base]:
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

		// Before the allowlist, so that an approved path cannot be used as a
		// mode laundering trick: ".claude/skills/pr-standards/SKILL.md" is
		// allowed as a file, not as a symlink to one. An alias also records
		// nothing for the paths underneath it, so an aliased directory exposes
		// configuration no path rule could otherwise see.
		case !isRegularFile(f) && inConfigDir:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleConfigNotFile,
				detail: fmt.Sprintf("agent and editor configuration paths must be real files; git records mode %s, which names content this repository does not carry and is not resolved here", f.mode),
			})

		// Default-deny. The directory match is case-insensitive so .Claude/
		// cannot evade it; the allowlist lookup is case-sensitive so a
		// near-miss spelling cannot inherit an approved path's approval.
		case inConfigDir && !allowedConfigFiles[p]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleUnlistedConfig,
				detail: "not on the reviewed allowlist in hack/verify-supply-chain/main.go",
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
// advice differs in kind and a single blanket line gets it wrong: an MCP or
// workspace file is unwanted and should go, an alias is usually a legitimate
// asset in the wrong shape, and an unlisted file may well belong here once
// somebody has read it.
var remediation = map[string]string{
	ruleUnlistedConfig: "If the file does not belong in the repository, remove it — agent and editor configuration belongs in your local working copy. If it genuinely does belong, say so in the PR: confirm it is needed, confirm it cannot execute anything by itself, have a second person read it in full, and add its exact path to allowedConfigFiles in hack/verify-supply-chain/main.go in the same PR. That edit is a change to a security control and will be reviewed as one.",
	ruleAgentSettings:  "Remove these from the commit. They belong in your local working copy only.",
	ruleEditorConfig:   "Remove these from the commit. They belong in your local working copy only.",
	ruleConfigNotFile:  "Replace the alias with a real file, or move the content out of the configuration path. Do not resolve it to find out what it points at.",
}

// report prints the findings, then what to do about each rule that appeared.
//
// Paths are quoted because git permits any byte in one except NUL and the
// separator, making an unquoted path a way for the reported-on thing to write
// the report: a newline forges lines, an ANSI sequence erases those above.
// Neither changes the exit code, but a security tool whose output its subject
// can write is not worth reading.
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
			// A new rule without advice is a bug, but a blank line here would
			// hide it. Say so rather than defaulting to "delete the file",
			// which is the wrong answer for two of the four rules.
			advice = "No remediation recorded for this rule; see docs/ci/supply-chain-presubmit.md."
		}
		fmt.Fprintf(w, "  %s: %s\n", rule, advice)
	}

	// Printed for every finding rather than decided per file. This
	// configuration is executable by design, and the check deliberately does
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
