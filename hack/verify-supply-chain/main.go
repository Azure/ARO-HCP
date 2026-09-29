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
// settings file, MCP server configuration, or an editor configuration file.
// Agent JSON carrying an execution key is reported as a known attack pattern.
//
// Only tracked files are inspected. Developers routinely keep gitignored agent
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

	for _, rel := range agentJSONFiles(files) {
		content, err := os.ReadFile(path.Join(root, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", rel, err)
			os.Exit(2)
		}
		for _, f := range scanAgentJSON(rel, content) {
			// A file the path rules already reject is blocked whatever its
			// syntax, so a second line saying it is also unparseable adds
			// nothing. The execution-key finding is never suppressed: it
			// escalates a merely unwanted file to a known attack pattern.
			if f.rule == ruleInvalidJSON && flagged[rel] {
				continue
			}
			findings = append(findings, f)
		}
	}

	if len(findings) == 0 {
		return
	}

	report(os.Stderr, findings)
	os.Exit(1)
}

// trackedFiles returns every file tracked by git, which excludes both
// untracked and gitignored files.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		files = append(files, string(entry))
	}
	return files, nil
}

// checkPaths applies the path-shaped rules. It is pure so that the denylist
// can be exercised without a git repository on disk.
//
// Every comparison is made against the lowercased path. Git's index is
// case-sensitive, so ".Claude/settings.json" is a distinct tracked path that
// would otherwise sidestep the denylist while being read just the same.
func checkPaths(files []string) []finding {
	var findings []finding
	for _, p := range files {
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
		case hasSegment(lower, editorConfigDir) && editorConfigFiles[base]:
			findings = append(findings, finding{
				path:   p,
				rule:   ruleEditorConfig,
				detail: "editor configuration files must not be committed",
			})
		}
	}
	return findings
}

// agentJSONFiles returns the tracked JSON whose contents need inspecting for
// execution keys: anything under an agent configuration directory, plus MCP
// server configuration wherever it sits.
func agentJSONFiles(files []string) []string {
	var out []string
	for _, p := range files {
		lower := strings.ToLower(p)
		switch {
		case mcpConfigFiles[path.Base(lower)]:
			out = append(out, p)
		case hasSegment(lower, agentConfigDir) && path.Ext(lower) == ".json":
			out = append(out, p)
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
