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

// verify-supply-chain inspects the files tracked by git and fails if any of
// them match a high-confidence supply-chain attack indicator: an AI-agent
// settings file, MCP server configuration, an editor workspace file, or
// anything at all under an editor configuration directory.
// Agent JSON carrying an execution key is reported as a known attack pattern.
// Agent JSON that cannot be parsed, or that is not a regular file and so
// cannot be read at all, is refused rather than given the benefit of the
// doubt, as is any entry standing in for an agent or editor configuration
// path rather than being one. Nothing is resolved to decide any of this: a
// rule that followed a link or fetched a submodule would be judging bytes the
// diff does not contain.
//
// Only tracked files are inspected, and they are inspected as git has them:
// paths and modes come from the index, contents from the object store, never
// from the working tree. Developers routinely keep gitignored agent
// configuration (for example .claude/settings.local.json) in their working
// copy, and that is not what this check is about.
//
// Scope is deliberately narrow. .claude/skills/ holds shared team tooling that
// CONTRIBUTING.md tells contributors to commit, so this does not police what
// kinds of file may live under an agent directory — that is a judgement for
// human review, which the reviewer guidance in .claude/skills/pr-standards/
// covers.
//
// Exit code is 1 if any violations are found, 2 on an internal error.
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

// agentConfigDir and editorConfigDir are matched as whole path segments at any
// depth. The repository's .gitignore only anchors these at the root, so
// frontend/.claude/settings.json would otherwise go unnoticed.
const (
	agentConfigDir  = ".claude"
	editorConfigDir = ".vscode"
)

// workspaceConfigExt is matched at any path rather than as a segment: an
// editor workspace file carries the same executable configuration as the
// directory above, under a name of the author's choosing and most often at
// the repository root.
const workspaceConfigExt = ".code-workspace"

// agentSettingsFiles are agent configuration files that grant an agent
// standing permission to execute things. They must never be committed.
var agentSettingsFiles = map[string]bool{
	"settings.json":       true,
	"settings.local.json": true,
	"mcp.json":            true,
}

// mcpConfigFiles name an MCP server set, every entry of which carries a
// command that the agent launches. These are matched by basename at any path,
// not under an agent directory: the project-scoped form sits at the repository
// root with no .claude segment to key off, and it is not gitignored. This
// repository already treats it as auto-discovered agent configuration — see
// the EnableConfigDiscovery comment in tooling/hcpctl/pkg/agent/agent.go.
// Matching by basename also covers .cursor/mcp.json and .vscode/mcp.json.
var mcpConfigFiles = map[string]bool{
	"mcp.json":  true,
	".mcp.json": true,
}

// executionKeys are JSON keys that make an agent configuration file
// self-executing. A .claude settings file carrying one of these matches a
// confirmed real-world malware pattern. The order is fixed so that a file
// carrying several of them always reports the same one.
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

	files, err := trackedFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing tracked files in %s: %v\n", root, err)
		os.Exit(2)
	}

	findings := checkPaths(files)

	flagged := make(map[string]bool, len(findings))
	for _, f := range findings {
		flagged[f.path] = true
	}

	for _, f := range agentJSONFiles(files) {
		var found []finding
		if readableBlob(f) {
			content, err := blobContent(root, f.oid)
			if err != nil {
				// Quoted for the same reason as in report: both values come
				// from the index.
				fmt.Fprintf(os.Stderr, "error reading %q (%q): %v\n", f.path, f.oid, err)
				os.Exit(2)
			}
			found = scanAgentJSON(f.path, content)
		} else {
			found = []finding{unreadableAgentJSON(f)}
		}

		for _, fd := range found {
			// A file the path rules already reject is blocked whatever its
			// syntax or type, so a second line saying it is also unparseable
			// or unreadable adds nothing. Only the execution-key finding
			// survives: it escalates a merely unwanted file to a known attack
			// pattern, which the reader needs to be told either way.
			if fd.rule != ruleExecutionKey && flagged[fd.path] {
				continue
			}
			findings = append(findings, fd)
		}
	}

	if len(findings) == 0 {
		return
	}

	report(os.Stderr, findings)
	os.Exit(1)
}

// Index modes git records for a tracked entry. Only a regular file — with or
// without the executable bit — carries its own bytes here; see readableBlob.
// The other two name content that lives somewhere else: a symlink's blob is a
// target path, and a gitlink is a commit in a different repository, which
// this one need not contain and a diff of it never shows.
const (
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
)

// trackedFile is one entry of the git index: the path, plus the mode and
// object ID git stores alongside it.
type trackedFile struct {
	path string
	mode string
	oid  string
}

// trackedFiles returns every entry in the git index.
//
// Untracked files are excluded, which is the point: developers routinely keep
// a gitignored .claude/settings.local.json in their working copy, and that is
// not what this check is about. Being gitignored is not an escape hatch,
// though — a force-added file is tracked from then on and git ls-files keeps
// returning it. The two SKILL.md files in this repository are exactly that,
// committed under a .gitignore'd /.claude/ directory.
func trackedFiles(root string) ([]trackedFile, error) {
	// --stage prints "<mode> <sha> <stage>\t<path>", so the entry's type is
	// read from the index rather than the working tree. That keeps the check
	// honest on a fresh clone and on filesystems that do not carry the
	// executable bit.
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

// blobContent returns the bytes git has recorded for an object ID.
//
// Content must come from the object store, not from the working tree. The two
// are not the same thing, and only one of them is what a PR proposes: an
// entry staged as a regular file can be swapped for a symlink to a fifo
// afterwards, leaving index mode 100644 while the path on disk hangs the
// reader, and a benign working-tree copy can just as easily mask a staged
// command key. Reading by object ID removes that gap by construction — the
// same bytes the index names are the bytes scanned — and it is also what
// makes the scan unhangable, since a blob is inert storage with no filesystem
// behaviour to trigger.
//
// One git invocation per file is fine at the scale this runs: the selector
// matches agent JSON only, of which this repository tracks none.
func blobContent(root, oid string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", oid)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// readableBlob reports whether an index entry names a blob worth scanning.
//
// This is a policy rule, not a safety guard — blobContent cannot hang however
// it is called, so nothing here is protecting the reader. What it decides is
// that agent JSON which is not a plain file gets refused rather than
// interpreted. For a symlink the blob is a target path, and scanning that
// string would be answering the wrong question: an agent resolves the link
// and reads whatever is on the other end, so the entry delivers its target's
// hooks and command keys while a scan of the link itself finds nothing. For a
// submodule the object is a commit that need not exist in this repository at
// all. Neither can be judged here, so both are reported — see
// unreadableAgentJSON — and left to a human.
//
// checkPaths asks the same question of a configuration path and refuses it
// for the same reason, so the two rules cannot drift into disagreeing about
// which entry types count as a file.
func readableBlob(f trackedFile) bool {
	return f.mode == modeRegular || f.mode == modeExecutable
}

// unreadableAgentJSON reports agent JSON whose contents the execution-key rule
// could not examine. The mode is quoted because it is the whole reason: a
// reviewer seeing it knows to look at what the entry stands for, which is the
// judgement this check deliberately leaves to a human.
//
// As the rules stand this never actually reaches a reader: everything
// agentJSONFiles selects either has a .claude segment, which ruleConfigNotFile
// rejects more descriptively, or an MCP basename, which ruleAgentSettings
// rejects outright — and main drops the duplicate. It is kept anyway, and
// deliberately not replaced with a skip. "Nothing can reach this" is a
// property of the other rules, not of this one, and the bypass fixed in
// 2003efa08 was exactly an unreachability argument that quietly stopped
// holding when the rule it depended on was removed. A non-regular entry is
// refused on its own terms here so that narrowing a path rule costs a
// duplicate finding rather than a silent hole.
func unreadableAgentJSON(f trackedFile) finding {
	return finding{
		path:   f.path,
		rule:   ruleUnreadable,
		detail: fmt.Sprintf("agent configuration must be a regular file; git records mode %s, which is not resolved here", f.mode),
	}
}

// checkPaths applies the path-shaped rules. It is pure so that the denylist
// can be exercised without a git repository on disk.
//
// Every comparison is made against the lowercased path. Git's index is
// case-sensitive, so ".Claude/settings.json" is a distinct tracked path that
// would otherwise sidestep the denylist while being read just the same.
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
		case hasSegment(lower, agentConfigDir) && agentSettingsFiles[base]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleAgentSettings,
				detail: "AI-agent settings files must not be committed",
			})
		// Matched by extension anywhere in the tree, because unlike the files
		// above this one has no fixed name and no required directory. A
		// .code-workspace is a single JSON document that can carry the entire
		// contents of .vscode/ — settings, extension recommendations, launch
		// configurations and tasks — and a task with runOptions.runOn set to
		// "folderOpen" runs when the workspace is opened. Workspace Trust
		// prompts first, but "the author clicked Trust" is not a control this
		// check should lean on. Conventionally the file sits at the
		// repository root, which is exactly where the editor directory rule
		// cannot see it.
		case path.Ext(lower) == workspaceConfigExt:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleEditorConfig,
				detail: "editor workspace files can run tasks on folder open and must not be committed",
			})

		// Before the editor directory rule, so that an entry the named-file
		// rules already describe keeps their more specific wording. What is
		// left for this case is what those rules structurally cannot see: a
		// configuration directory that is not a directory in this repository
		// at all. Git tracks "frontend/.claude -> config" as a single entry
		// with no extension and a basename on no denylist, and tracks a
		// submodule mounted at the same path the same way. Either records
		// nothing for the paths underneath, so frontend/.claude/settings.json
		// is never a tracked path and no amount of filename matching can
		// match it — yet that is the path an agent resolves and reads, from a
		// symlink target that keeps its own innocuous name or from a checkout
		// the parent repository's diff does not show.
		//
		// The test is readableBlob rather than a list of the two modes, so
		// that an entry type neither case anticipated is refused here too.
		case !readableBlob(f) && (hasSegment(lower, agentConfigDir) || hasSegment(lower, editorConfigDir)):
			findings = append(findings, finding{
				path:   p,
				rule:   ruleConfigNotFile,
				detail: fmt.Sprintf("agent and editor configuration paths must be real files; git records mode %s, which names content this repository does not carry and is not resolved here", f.mode),
			})

		// Last, and deliberately not a list of filenames. Several files under
		// .vscode/ execute on folder open — tasks.json via runOptions.runOn,
		// launch.json, settings.json through terminal profiles — but
		// enumerating them only has to miss one, and the set is Microsoft's to
		// grow. Nothing under .vscode/ is legitimately tracked in this
		// repository (.gitignore excludes it, and no such path is tracked
		// today), so the directory itself is the rule and no filename needs
		// judging. A tracked .vscode/ entry that is genuinely wanted is a
		// deliberate, reviewed change to this check.
		case hasSegment(lower, editorConfigDir):
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
// anything under an agent configuration directory, plus MCP server
// configuration wherever it sits.
//
// Selection is by path alone. Entries that cannot be opened are still
// selected, so that the caller reports them rather than dropping them; that
// the mode decides how an entry is handled, not whether it is considered at
// all, is what keeps a symlink from being a way out of this rule.
func agentJSONFiles(files []trackedFile) []trackedFile {
	var out []trackedFile
	for _, f := range files {
		lower := strings.ToLower(f.path)
		switch {
		case mcpConfigFiles[path.Base(lower)]:
			out = append(out, f)
		case hasSegment(lower, agentConfigDir) && path.Ext(lower) == ".json":
			out = append(out, f)
		}
	}
	return out
}

// scanAgentJSON requires agent JSON to parse, then reports an execution key
// anywhere in it.
//
// Rejecting unparseable content is what keeps this rule honest. The obvious
// evasion against a check that decodes JSON is to break the syntax: a single
// trailing comma makes the decoder give up, and anything that then scans the
// raw bytes has to tell a key from a value and a comment from a string by
// hand, guessing at what some other parser would have made of it. Guessing
// wrong in this direction is expensive, because a hit here tells an author
// their file matches confirmed malware.
//
// So malformed input is not scanned more cleverly, it is refused. An attacker
// is left with valid JSON, where the decoder is authoritative, or invalid
// JSON, which never reaches the key walk. There is no third case to harden
// against later.
//
// Strict encoding/json means a comment makes a file invalid, which is how
// .devcontainer/devcontainer.json is written. No .json is tracked under an
// agent directory today, and the files most likely to want comments —
// settings.json, mcp.json — are rejected by path regardless, so nothing turns
// on it. Should a legitimate agent JSON ever need comments, teach this
// function JSONC deliberately rather than restoring a guess.
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

// findExecutionKey walks a decoded JSON document for the first execution key.
// Keys are visited in sorted order so that a document carrying several of them
// always reports the same one.
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

// hasSegment reports whether segment appears as a whole path element, so that
// ".claude" matches "frontend/.claude/x" but not "notclaude/x". Callers pass a
// lowercased path; every denylist entry in this file is lowercase.
func hasSegment(p, segment string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

// report prints the findings. Paths are quoted with %q rather than printed
// raw, because a path is attacker-chosen data and git permits any byte in one
// except NUL and the separator.
//
// An unquoted path is a way to write to this report. A directory named with a
// newline splits one finding into what reads as several lines of independent
// output, and one named with an ANSI sequence can move the cursor up and
// erase the genuine findings above it in any viewer that interprets escapes.
// Neither changes the exit code — the check still fails, and nothing here can
// make it pass — but a security tool whose output can be written by the thing
// it is reporting on is not worth reading. %q also keeps ordinary paths
// legible: Go escapes only non-printable characters, so a path with
// non-ASCII letters in it survives intact.
//
// The rest of each line is safe already. The rule is one of this file's own
// constants; a detail embeds an execution key with %q, an index mode git
// constrains to a known set, and a decoder error that encoding/json has
// itself escaped.
func report(w io.Writer, findings []finding) {
	fmt.Fprintln(w, "ERROR: supply-chain attack indicators found in tracked files.")
	fmt.Fprintln(w)

	malware := false
	for _, f := range findings {
		fmt.Fprintf(w, "  %q (%s): %s\n", f.path, f.rule, f.detail)
		if f.malware {
			malware = true
			fmt.Fprintln(w, `      This matches a known supply-chain attack pattern. Do not run it.`)
			fmt.Fprintln(w, "      Report it to the security team before taking any other action.")
		}
	}

	fmt.Fprintln(w)
	if malware {
		fmt.Fprintln(w, "Do not interact with the files flagged as attack patterns above.")
	}
	fmt.Fprintln(w, `If these files are yours, remove them from the commit — they belong in`)
	fmt.Fprintln(w, `your local working copy only. See the "Security self-check" bullet in`)
	fmt.Fprintln(w, "CONTRIBUTING.md.")
}
