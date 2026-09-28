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
// settings file, an editor configuration file, or an executable payload
// dropped into an agent configuration directory.
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

// executableExtensions are script types that have no business living inside an
// agent configuration directory. Markdown is deliberately absent: checked-in
// skills under .claude/skills/ are expected.
var executableExtensions = map[string]bool{
	".sh":   true,
	".bash": true,
	".mjs":  true,
	".cjs":  true,
	".js":   true,
	".ts":   true,
	".py":   true,
	".rb":   true,
	".ps1":  true,
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
	ruleAgentSettings   = "agent-settings"
	ruleEditorConfig    = "editor-config"
	ruleAgentExecutable = "agent-executable"
	ruleExecutionKey    = "execution-key"
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

// trackedFiles returns the repository-relative paths of every file tracked by
// git, which excludes both untracked and gitignored files.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
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
func checkPaths(files []string) []finding {
	var findings []finding
	for _, f := range files {
		lower := strings.ToLower(f)
		base := path.Base(lower)
		switch {
		case hasSegment(lower, agentConfigDir) && agentSettingsFiles[base]:
			findings = append(findings, finding{
				path:   f,
				rule:   ruleAgentSettings,
				detail: "AI-agent settings files must not be committed",
			})
		case hasSegment(lower, editorConfigDir) && editorConfigFiles[base]:
			findings = append(findings, finding{
				path:   f,
				rule:   ruleEditorConfig,
				detail: "editor configuration files must not be committed",
			})
		case hasSegment(lower, agentConfigDir) && executableExtensions[path.Ext(base)]:
			findings = append(findings, finding{
				path:   f,
				rule:   ruleAgentExecutable,
				detail: "executable scripts must not live in an agent configuration directory",
			})
		}
	}
	return findings
}

// agentJSONFiles returns the tracked JSON files under an agent configuration
// directory, whose contents need inspecting for execution keys.
func agentJSONFiles(files []string) []string {
	var out []string
	for _, f := range files {
		lower := strings.ToLower(f)
		if hasSegment(lower, agentConfigDir) && path.Ext(lower) == ".json" {
			out = append(out, f)
		}
	}
	return out
}

// scanAgentJSON reports execution keys anywhere in an agent JSON file. A file
// that does not parse is scanned as raw text instead, so that deliberately
// malformed JSON cannot slip past the decoder.
func scanAgentJSON(p string, content []byte) []finding {
	var doc any
	if err := json.Unmarshal(content, &doc); err != nil {
		for _, key := range executionKeys {
			if bytes.Contains(content, []byte(`"`+key+`"`)) {
				return []finding{{
					path:    p,
					rule:    ruleExecutionKey,
					detail:  fmt.Sprintf("malformed JSON containing a %q key", key),
					malware: true,
				}}
			}
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
