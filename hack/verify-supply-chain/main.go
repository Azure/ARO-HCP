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
// settings file, MCP server configuration, or editor configuration. Agent JSON
// carrying an execution key is reported as a known attack pattern. Exit code is
// 1 for a violation, 2 on an internal error.
//
// Nothing is resolved to decide this: paths and modes come from the git index
// and contents from the object store, so the check judges exactly the bytes a
// PR proposes, and refuses anything it cannot judge that way.
//
// Threat model, per-rule reasoning, and the scope left to human review:
// docs/ci/supply-chain-presubmit.md.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
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

// executionKeys make an agent configuration file self-executing. The order is
// fixed so a file carrying several always reports the same one.
var executionKeys = []string{"command", "hooks"}

func isExecutionKey(key string) bool {
	return slices.Contains(executionKeys, key)
}

const (
	ruleAgentSettings = "agent-settings"
	ruleEditorConfig  = "editor-config"
	ruleExecutionKey  = "execution-key"
	ruleInvalidJSON   = "invalid-json"
	ruleUnreadable    = "unreadable"
	ruleConfigNotFile = "config-not-a-file"
)

// finding is a single violation. malware marks the indicators that match a
// known attack pattern rather than merely being unwanted.
type finding struct {
	path    string
	rule    string
	detail  string
	malware bool
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

// scanRepository applies every rule to the git index at root.
//
// Separate from main so the rules can be tested as they actually run. They
// interact — the content rules read exactly what the path rules block — and
// testing each in isolation is what let two of them go unreachable without a
// test noticing.
func scanRepository(root string) ([]finding, error) {
	files, err := trackedFiles(root)
	if err != nil {
		return nil, fmt.Errorf("error listing tracked files in %s: %w", root, err)
	}

	findings := checkPaths(files)

	// One entry can earn a path finding and a content finding, and both are
	// reported. They say different things: the path rule that the file does
	// not belong in the commit, the content rule what the scan made of the
	// bytes. Suppressing the second because the first already fails the build
	// is what made invalid-json and unreadable unreachable, and it hid the one
	// signal separating a stray settings.json from an attempt to smuggle a
	// command key past the decoder by breaking the syntax.
	for _, f := range agentJSONFiles(files) {
		if !readableBlob(f) {
			findings = append(findings, unreadableAgentJSON(f))
			continue
		}

		content, err := blobContent(root, f.oid)
		if err != nil {
			// Quoted for the same reason as in report: index data.
			return nil, fmt.Errorf("error reading %q (%q): %w", f.path, f.oid, err)
		}
		findings = append(findings, scanAgentJSON(f.path, content)...)
	}

	return findings, nil
}

// Index modes. Only a regular file carries its own bytes; a symlink's blob is
// a target path and a gitlink is a commit in another repository.
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
	oid  string
}

// trackedFiles returns every entry in the git index. Untracked files are
// excluded by design — a gitignored settings.local.json in a working copy is
// not what this check is about — but gitignoring is no escape hatch either: a
// force-added file stays tracked and keeps appearing here.
func trackedFiles(root string) ([]trackedFile, error) {
	// --stage prints "<mode> <sha> <stage>\t<path>", so the entry's type comes
	// from the index rather than the working tree.
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

// blobContent returns the bytes git recorded for an object ID. Never
// os.ReadFile: an entry staged as a regular file can be swapped on disk
// afterwards, leaving the index mode intact while the path hangs the reader or
// hides the staged bytes.
func blobContent(root, oid string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", oid)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// readableBlob reports whether an index entry carries its own bytes. A
// symlink's blob is a target path rather than the target, and a submodule's
// object is a commit this repository need not have, so neither can be judged
// here. checkPaths asks the same question so the two cannot drift.
func readableBlob(f trackedFile) bool {
	return f.mode == modeRegular || f.mode == modeExecutable
}

// unreadableAgentJSON refuses agent JSON the execution-key rule could not read,
// naming the mode so a reviewer knows what the entry stands for. The path rules
// reject the same entry, and both lines are printed: that one says the alias
// may not be committed, this one that nothing here judged what it stands for.
func unreadableAgentJSON(f trackedFile) finding {
	return finding{
		path:   f.path,
		rule:   ruleUnreadable,
		detail: fmt.Sprintf("agent configuration must be a regular file; git records mode %s, which is not resolved here", f.mode),
	}
}

// checkPaths applies the path-shaped rules. Pure, so the denylist is testable
// without a repository on disk. Comparisons are against the lowercased path:
// git's index is case-sensitive, so .Claude/settings.json is a distinct
// tracked path. Case order is load-bearing; read the comments before moving.
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
		// reads. Keyed on readableBlob so an unanticipated mode is refused too.
		case !readableBlob(f) && hasAnySegment(lower, configDirs...):
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

// agentJSONFiles returns the tracked JSON subject to the execution-key rule:
// exactly the names checkPaths already blocks. Reading them decides whether the
// report says "must not be committed" or "matches confirmed malware", so this
// escalates severity rather than detecting anything new.
//
// Deliberately not every .json under an agent directory. Nothing auto-loads a
// skill's fixtures or manifests, so a command key in one is far more likely to
// be a documented example than an attack — and telling an author their test
// data is confirmed malware is the one mistake this rule must not make. The
// route it appears to cover it cannot: a malicious skill carries its
// instruction in SKILL.md, which is markdown this check does not read.
//
// Keyed on the filename, not the directory, so .claude/skills/x/settings.json
// is still escalated. An allowlisted directory would have been a safe harbour.
//
// Unreadable entries are selected too, so that the caller refuses them.
// Dropping them here would be the bypass.
func agentJSONFiles(files []trackedFile) []trackedFile {
	var out []trackedFile
	for _, f := range files {
		lower := strings.ToLower(f.path)
		base := path.Base(lower)
		switch {
		case mcpConfigFiles[base]:
			out = append(out, f)
		case hasAnySegment(lower, agentConfigDirs...) && agentSettingsFiles[base]:
			out = append(out, f)
		}
	}
	return out
}

// scanAgentJSON requires agent JSON to parse, then reports an execution key
// anywhere in it.
//
// Malformed input is refused, not scanned more cleverly: a raw-byte fallback
// has to tell a key from a value by hand, and a wrong guess here tells an
// author their file is confirmed malware.
func scanAgentJSON(p string, content []byte) []finding {
	var doc any
	if err := json.Unmarshal(content, &doc); err != nil {
		return []finding{{
			path:   p,
			rule:   ruleInvalidJSON,
			detail: fmt.Sprintf("agent configuration must be valid JSON: %v", err),
		}}
	}

	if key, ok := findExecutionKey(doc); ok {
		return []finding{{
			path:    p,
			rule:    ruleExecutionKey,
			detail:  fmt.Sprintf("contains a %q key", key),
			malware: true,
		}}
	}
	return nil
}

// findExecutionKey walks a decoded JSON document for the first execution key,
// visiting keys in sorted order so the same one is always reported.
func findExecutionKey(node any) (string, bool) {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if isExecutionKey(key) {
				return key, true
			}
			if found, ok := findExecutionKey(v[key]); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range v {
			if found, ok := findExecutionKey(child); ok {
				return found, true
			}
		}
	}
	return "", false
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
// advice differs in kind and a single blanket line gets most of it wrong: a
// settings file is unwanted and should go, whereas an alias is usually a
// legitimate asset in the wrong shape and telling its author to delete it
// costs them work the check never meant to reject. A syntax error is different
// again — it is the one finding that reports the scan did not run, so the
// advice has to point at the contents rather than at the comma.
var remediation = map[string]string{
	ruleAgentSettings: "Remove these from the commit. They belong in your local working copy only.",
	ruleEditorConfig:  "Remove these from the commit. They belong in your local working copy only.",
	ruleConfigNotFile: "Replace the alias with a real file, or move the content out of the configuration path. Do not resolve it to find out what it points at.",
	ruleInvalidJSON:   "This did not parse as strict JSON — no comments, no trailing commas — so it was never scanned for an execution key. Breaking the syntax is how a command key gets past the decoder, so read the contents rather than assuming a stray comma.",
	ruleUnreadable:    "Replace the entry with a real file. Its contents could not be read, so the execution-key rule could not judge them.",
	ruleExecutionKey:  "Do not open or run these files. Report them to the security team before taking any other action.",
}

// report prints the findings, then what to do about each rule that appeared.
//
// Paths are quoted because git permits any byte in one except NUL and the
// separator, making an unquoted path a way for the reported-on thing to write
// the report: a newline forges lines, an ANSI sequence erases those above.
// Neither changes the exit code, but a security tool whose output its subject
// can write is not worth reading.
func report(w io.Writer, findings []finding) {
	fmt.Fprintln(w, "ERROR: supply-chain attack indicators found in tracked files.")
	fmt.Fprintln(w)

	var rules []string
	for _, f := range findings {
		fmt.Fprintf(w, "  %q (%s): %s\n", f.path, f.rule, f.detail)
		if f.malware {
			fmt.Fprintln(w, `      This matches a known supply-chain attack pattern. Do not run it.`)
			fmt.Fprintln(w, "      Report it to the security team before taking any other action.")
		}
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
			// which is the wrong answer for half the rules.
			advice = "No remediation recorded for this rule; see docs/ci/supply-chain-presubmit.md."
		}
		fmt.Fprintf(w, "  %s: %s\n", rule, advice)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, `See the "Security self-check" bullet in CONTRIBUTING.md, and`)
	fmt.Fprintln(w, "docs/ci/supply-chain-presubmit.md for why each rule exists.")
}
