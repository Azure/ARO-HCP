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

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestCheckPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		mode string // empty means a regular file
		rule string // empty means the path must be accepted
	}{
		{name: "root agent settings", path: ".claude/settings.json", rule: ruleAgentSettings},
		{name: "nested agent settings", path: "frontend/.claude/settings.json", rule: ruleAgentSettings},
		{name: "deeply nested agent settings", path: "a/b/c/.claude/settings.json", rule: ruleAgentSettings},
		{name: "local agent settings", path: ".claude/settings.local.json", rule: ruleAgentSettings},
		{name: "agent mcp config", path: ".claude/mcp.json", rule: ruleAgentSettings},
		{name: "editor settings", path: ".vscode/settings.json", rule: ruleEditorConfig},
		{name: "editor extensions", path: ".vscode/extensions.json", rule: ruleEditorConfig},
		{name: "nested editor tasks", path: "a/b/.vscode/tasks.json", rule: ruleEditorConfig},
		{name: "editor launch", path: ".vscode/launch.json", rule: ruleEditorConfig},

		// The editor rule is the directory, not a list of filenames: every
		// one of these used to pass, and each is either executable on open or
		// a place to hide something that is. Enumerating filenames only has
		// to miss once, and the set belongs to Microsoft.
		{name: "editor keybindings", path: ".vscode/keybindings.json", rule: ruleEditorConfig},
		{name: "editor c_cpp properties", path: ".vscode/c_cpp_properties.json", rule: ruleEditorConfig},
		{name: "editor readme", path: ".vscode/README.md", rule: ruleEditorConfig},
		{name: "script in editor directory", path: ".vscode/setup.sh", rule: ruleEditorConfig},
		{name: "nested file in editor directory", path: ".vscode/profiles/default.json", rule: ruleEditorConfig},
		{name: "unknown future editor file", path: ".vscode/whatever-ships-next.json", rule: ruleEditorConfig},

		// An editor workspace file carries everything .vscode/ does, under a
		// name its author picks and usually at the repository root, so it is
		// matched by extension at any path rather than under a directory.
		{name: "workspace file at root", path: "aro-hcp.code-workspace", rule: ruleEditorConfig},
		{name: "nested workspace file", path: "docs/team.code-workspace", rule: ruleEditorConfig},
		{name: "workspace file in editor directory", path: ".vscode/x.code-workspace", rule: ruleEditorConfig},
		{name: "workspace file with dotted name", path: "my.project.code-workspace", rule: ruleEditorConfig},
		{name: "uppercased workspace file", path: "Team.Code-Workspace", rule: ruleEditorConfig},
		{name: "workspace file in agent directory", path: ".claude/x.code-workspace", rule: ruleEditorConfig},

		// MCP server config is matched by basename at any path: the
		// project-scoped form has no .claude segment to key off.
		{name: "project-scoped mcp config", path: ".mcp.json", rule: ruleAgentSettings},
		{name: "nested project-scoped mcp config", path: "frontend/.mcp.json", rule: ruleAgentSettings},
		{name: "cursor mcp config", path: ".cursor/mcp.json", rule: ruleAgentSettings},
		{name: "editor mcp config", path: ".vscode/mcp.json", rule: ruleAgentSettings},
		{name: "uppercased project-scoped mcp config", path: ".MCP.json", rule: ruleAgentSettings},

		// Git's index is case-sensitive, so each of these is a distinct
		// tracked path that must not be able to sidestep the denylist.
		{name: "uppercased settings basename", path: ".claude/Settings.json", rule: ruleAgentSettings},
		{name: "uppercased agent directory", path: ".Claude/settings.json", rule: ruleAgentSettings},
		{name: "uppercased editor directory", path: ".VSCode/extensions.json", rule: ruleEditorConfig},
		{name: "fully uppercased path", path: "FRONTEND/.CLAUDE/SETTINGS.JSON", rule: ruleAgentSettings},

		// Deliberately accepted. CONTRIBUTING.md tells contributors to commit
		// shared tooling to .claude/skills/, and the review guidance this
		// check automates says in the same breath that changes there "are
		// expected". Judging which files may live under an agent directory is
		// left to human review; automating it blocked ordinary skill assets
		// such as OWNERS files and screenshots.
		{name: "checked-in skill", path: ".claude/skills/pr-standards/SKILL.md"},
		{name: "skill reference doc", path: ".claude/skills/x/reference.md"},
		{name: "skill data file", path: ".claude/skills/x/data.yaml"},
		{name: "skill owners file", path: ".claude/skills/x/OWNERS"},
		{name: "skill screenshot", path: ".claude/skills/x/images/flow.png"},
		{name: "skill helper script", path: ".claude/skills/x/scripts/build.py"},
		{name: "shell script in agent dir", path: ".claude/setup.sh"},
		{name: "non-settings agent json", path: ".claude/skills/x/meta.json"},

		// A configuration directory that is not one. Git records a single
		// entry for the link itself, with no extension and a basename on no
		// denylist, and nothing at all for the paths it exposes --
		// "frontend/.claude -> config" means frontend/.claude/settings.json
		// is never a tracked path, so no named-file rule can ever see it,
		// while an agent resolves exactly that path and reads whatever
		// config/ holds. The link is rejected without being followed.
		{name: "symlinked agent directory", path: "frontend/.claude", mode: modeSymlink, rule: ruleConfigNotFile},
		{name: "symlinked root agent directory", path: ".claude", mode: modeSymlink, rule: ruleConfigNotFile},
		{name: "symlinked editor directory", path: "frontend/.vscode", mode: modeSymlink, rule: ruleConfigNotFile},
		{name: "symlinked nested editor directory", path: "a/b/.vscode", mode: modeSymlink, rule: ruleConfigNotFile},
		{name: "uppercased symlinked agent directory", path: "frontend/.Claude", mode: modeSymlink, rule: ruleConfigNotFile},
		// A symlink deeper inside an agent directory aliases just as well, so
		// the rule keys off the segment rather than the final element.
		{name: "symlinked skill subdirectory", path: ".claude/skills/x", mode: modeSymlink, rule: ruleConfigNotFile},
		{name: "symlinked file in agent directory", path: ".claude/skills/x/meta.json", mode: modeSymlink, rule: ruleConfigNotFile},
		// Named-file rules are more specific, so they keep their wording when
		// both could apply.
		{name: "symlinked agent settings", path: ".claude/settings.json", mode: modeSymlink, rule: ruleAgentSettings},
		{name: "symlinked project-scoped mcp config", path: ".mcp.json", mode: modeSymlink, rule: ruleAgentSettings},

		// A submodule aliases a configuration path just as effectively, and
		// hides the content better: a symlink at least names its target in
		// the diff, whereas a gitlink is a commit ID in some other
		// repository that the parent's diff never shows. The rule therefore
		// keys off "not a regular file" rather than off the symlink mode.
		{name: "submodule at agent directory", path: "frontend/.claude", mode: modeSubmodule, rule: ruleConfigNotFile},
		{name: "submodule at root agent directory", path: ".claude", mode: modeSubmodule, rule: ruleConfigNotFile},
		{name: "submodule at editor directory", path: "frontend/.vscode", mode: modeSubmodule, rule: ruleConfigNotFile},
		{name: "submodule inside agent directory", path: ".claude/skills/x", mode: modeSubmodule, rule: ruleConfigNotFile},
		{name: "uppercased submodule at agent directory", path: "frontend/.Claude", mode: modeSubmodule, rule: ruleConfigNotFile},
		// An unrecognised mode is refused for the same reason rather than
		// falling through: the rule is "a real file", not "not one of these".
		{name: "unknown mode at agent directory", path: "frontend/.claude", mode: "040000", rule: ruleConfigNotFile},

		// Ordinary skill assets are regular files, executable or not, and
		// stay unaffected; an aliased path elsewhere in the tree is none of
		// this check's business.
		{name: "executable skill helper", path: ".claude/skills/x/build.py", mode: modeExecutable},
		{name: "symlink outside config directories", path: "docs/latest", mode: modeSymlink},
		{name: "symlink in similarly named directory", path: "notclaude/link", mode: modeSymlink},
		{name: "submodule outside config directories", path: "vendor/thirdparty", mode: modeSubmodule},

		{name: "similarly named file", path: "config/mcp.json.tmpl"},
		// The workspace rule is the final extension, not a substring: docs
		// about workspace files are ordinary content.
		{name: "doc about a workspace file", path: "docs/setup.code-workspace.md"},
		{name: "workspace extension as a directory", path: "code-workspace/notes.md"},
		{name: "devcontainer config", path: ".devcontainer/devcontainer.json"},
		{name: "devcontainer script", path: ".devcontainer/postCreate.sh"},
		{name: "unrelated settings file", path: "config/settings.json"},
		{name: "unrelated extensions file", path: "docs/extensions.json"},
		{name: "similarly named directory", path: "notclaude/settings.json"},
		{name: "ordinary shell script", path: "hack/verify.sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			if mode == "" {
				mode = modeRegular
			}
			findings := checkPaths([]trackedFile{{path: tc.path, mode: mode}})

			if tc.rule == "" {
				if len(findings) != 0 {
					t.Fatalf("expected %s to be accepted, got %+v", tc.path, findings)
				}
				return
			}

			if len(findings) != 1 {
				t.Fatalf("expected exactly one finding for %s, got %+v", tc.path, findings)
			}
			if findings[0].rule != tc.rule {
				t.Errorf("expected rule %q for %s, got %q", tc.rule, tc.path, findings[0].rule)
			}
			if findings[0].path != tc.path {
				t.Errorf("expected path %q, got %q", tc.path, findings[0].path)
			}
		})
	}
}

func TestCheckPathsReportsEveryViolation(t *testing.T) {
	findings := checkPaths(regularFiles(
		"README.md",
		".claude/settings.json",
		"frontend/.vscode/extensions.json",
		".claude/skills/pr-standards/SKILL.md",
		".mcp.json",
	))

	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %+v", findings)
	}
}

// regularFiles builds index entries for paths whose mode is not what the test
// is about.
func regularFiles(paths ...string) []trackedFile {
	files := make([]trackedFile, len(paths))
	for i, p := range paths {
		files[i] = trackedFile{path: p, mode: modeRegular}
	}
	return files
}

func TestAgentJSONFiles(t *testing.T) {
	got := agentJSONFiles(append(regularFiles(
		".claude/settings.json",
		".claude/settings.local.json",
		".claude/skills/x/SKILL.md",
		"config/config.json",
		".vscode/settings.json",
		// Keyed on the filename, not the directory: an auto-loaded name is
		// escalated wherever it sits, so .claude/skills/ is no safe harbour.
		".claude/skills/x/settings.json",
		// Case-insensitive, because git's index is not.
		".Claude/Settings.JSON",
		// MCP config is scanned wherever it sits, so that a command hidden in
		// a project-scoped server entry still reaches the content rules.
		".mcp.json",
		".cursor/mcp.json",

		// Not selected. Nothing auto-loads a skill's fixtures or manifests, so
		// a command key in one is a documented example far more often than an
		// attack, and this rule reports confirmed malware. Scanning them
		// bought no coverage either: a malicious skill carries its instruction
		// in SKILL.md, which is markdown this check does not read.
		".claude/skills/x/fixture.json",
		".claude/skills/x/testdata.json",
		".claude/skills/x/images/manifest.json",
		"frontend/.claude/other.json",
	),
		// Selected despite not being readable. A symlink is how an execution
		// key would otherwise walk straight through this rule: git stores a
		// target path, but an agent resolves the link and reads the keys on
		// the other end. Selecting it here is what lets the caller refuse it;
		// dropping it at selection time is the bypass.
		trackedFile{path: "frontend/.claude/settings.json", mode: modeSymlink},
		trackedFile{path: "frontend/.mcp.json", mode: modeSymlink},
		trackedFile{path: "a/.claude/mcp.json", mode: modeSubmodule},
		// An executable regular file is still a plain blob and safe to read.
		trackedFile{path: ".claude/settings.local.json", mode: modeExecutable},
		// A non-auto-loaded name stays unselected whatever its mode.
		trackedFile{path: ".claude/skills/x/meta.json", mode: modeSymlink},
	))

	want := []string{
		".claude/settings.json",
		".claude/settings.local.json",
		".claude/skills/x/settings.json",
		".Claude/Settings.JSON",
		".mcp.json",
		".cursor/mcp.json",
		"frontend/.claude/settings.json",
		"frontend/.mcp.json",
		"a/.claude/mcp.json",
		".claude/settings.local.json",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i].path != want[i] {
			t.Errorf("index %d: expected %q, got %q", i, want[i], got[i].path)
		}
	}
}

// TestSkillAssetsAreNotContentScanned pins the carve-out the selector exists
// for. These are the exact shapes a skill legitimately ships -- a manifest
// documenting example commands, test data for a hooks-related skill -- and
// before the selector was narrowed each was reported as confirmed malware and
// its author sent to the security team.
func TestSkillAssetsAreNotContentScanned(t *testing.T) {
	for _, p := range []string{
		".claude/skills/x/fixture.json",
		".claude/skills/x/testdata.json",
		".claude/skills/hooks-docs/examples.json",
		"frontend/.claude/notes.json",
	} {
		t.Run(p, func(t *testing.T) {
			files := regularFiles(p)

			if selected := agentJSONFiles(files); len(selected) != 0 {
				t.Fatalf("expected %s not to be content-scanned, got %+v", p, selected)
			}
			if findings := checkPaths(files); len(findings) != 0 {
				t.Fatalf("expected %s to be accepted by the path rules, got %+v", p, findings)
			}
		})
	}
}

// TestReadableBlob pins which index modes are treated as scannable agent
// JSON. Everything else is refused unread by unreadableAgentJSON.
//
// Widening this set is no longer a hang risk — blobContent reads the object
// store, which has no filesystem behaviour to trigger — but it is still a
// correctness one. Admitting 120000 would scan a symlink's blob, and that
// blob is the target path, not the target: the scan would read
// "../../elsewhere.json", find no execution key, and pass the entry, while an
// agent resolves the link and gets whatever the target holds. That is exactly
// the bypass unreadableAgentJSON exists to close, so a mode added here needs
// an answer for what its blob actually contains.
func TestReadableBlob(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{mode: modeRegular, want: true},
		{mode: modeExecutable, want: true},
		{mode: modeSymlink},
		{mode: modeSubmodule},
		{mode: ""}, // an index entry that failed to parse
	} {
		t.Run(tc.mode, func(t *testing.T) {
			if got := readableBlob(trackedFile{path: ".claude/x.json", mode: tc.mode}); got != tc.want {
				t.Errorf("mode %q: expected %v, got %v", tc.mode, tc.want, got)
			}
		})
	}
}

// TestUnreadableAgentJSON checks the wording of the finding a non-regular
// entry produces. It must not claim malware: an unreadable file is one this
// check declined to judge, not one it convicted.
func TestUnreadableAgentJSON(t *testing.T) {
	got := unreadableAgentJSON(trackedFile{path: ".claude/skills/x/meta.json", mode: modeSymlink})

	if got.rule != ruleUnreadable {
		t.Errorf("expected rule %q, got %q", ruleUnreadable, got.rule)
	}
	if got.path != ".claude/skills/x/meta.json" {
		t.Errorf("unexpected path %q", got.path)
	}
	if got.malware {
		t.Error("an unreadable entry is not a confirmed attack pattern")
	}
	if !strings.Contains(got.detail, "120000") {
		t.Errorf("expected the detail to name the mode, got %q", got.detail)
	}
}

func TestScanAgentJSON(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     string
		wantRule string // empty means the file must be accepted
	}{
		{name: "hook with command key", file: "settings-with-hook-command.json", wantRule: ruleExecutionKey},
		{name: "permissions only", file: "settings-benign.json"},

		// Syntax decides these, not content. Breaking the JSON is the obvious
		// way to stop a decoder finding an execution key, so unparseable input
		// is refused outright rather than scanned by some weaker method. Both
		// fixtures are malformed and only one carries a command key; they are
		// treated identically because the scanner never gets far enough to
		// tell them apart.
		{name: "malformed json hiding a command key", file: "settings-malformed.json", wantRule: ruleInvalidJSON},
		{name: "malformed json with no command key", file: "settings-malformed-benign.json", wantRule: ruleInvalidJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}

			findings := scanAgentJSON(".claude/settings.json", content)

			if tc.wantRule == "" {
				if len(findings) != 0 {
					t.Fatalf("expected no findings, got %+v", findings)
				}
				return
			}

			if len(findings) != 1 {
				t.Fatalf("expected exactly one finding, got %+v", findings)
			}
			if findings[0].rule != tc.wantRule {
				t.Fatalf("expected rule %q, got %q", tc.wantRule, findings[0].rule)
			}

			// Only a decoded execution key is a confirmed attack pattern.
			// Unparseable content must not carry that wording: it would tell
			// an author whose file merely has a stray comma that they are
			// looking at malware.
			wantMalware := tc.wantRule == ruleExecutionKey
			if findings[0].malware != wantMalware {
				t.Errorf("expected malware=%v, got %+v", wantMalware, findings[0])
			}
		})
	}
}

// TestScanAgentJSONAcceptsOnlyStrictJSON pins the syntax gate. JSON-with-
// comments is how .devcontainer/devcontainer.json is written, so rejecting it
// is a deliberate choice and not an accident to be quietly relaxed.
func TestScanAgentJSONAcceptsOnlyStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wantOK  bool
	}{
		{name: "strict json", content: `{"permissions": {"allow": []}}`, wantOK: true},
		{name: "empty object", content: `{}`, wantOK: true},
		{name: "trailing comma", content: `{"a": 1,}`},
		{name: "line comment", content: "{\n// note\n\"a\": 1\n}"},
		{name: "block comment", content: `{"a": /* note */ 1}`},
		{name: "unterminated string", content: `{"a": "x`},
		{name: "empty file", content: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := scanAgentJSON(".claude/skills/x/meta.json", []byte(tc.content))

			if tc.wantOK {
				if len(findings) != 0 {
					t.Fatalf("expected %s to be accepted, got %+v", tc.content, findings)
				}
				return
			}
			if len(findings) != 1 || findings[0].rule != ruleInvalidJSON {
				t.Fatalf("expected an %s finding for %s, got %+v", ruleInvalidJSON, tc.content, findings)
			}
		})
	}
}

func TestFindExecutionKeyNested(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  any
		want string
	}{
		{
			name: "command nested in array",
			doc:  map[string]any{"a": []any{map[string]any{"command": "x"}}},
			want: "command",
		},
		{
			// Both keys are present, so the result would flap with map
			// iteration order if the keys were not checked in a fixed order.
			name: "both execution keys reports deterministically",
			doc:  map[string]any{"hooks": map[string]any{}, "command": "x"},
			want: "command",
		},
		{
			name: "hooks at top level",
			doc:  map[string]any{"hooks": map[string]any{}},
			want: "hooks",
		},
		{
			name: "command as a value not a key",
			doc:  map[string]any{"type": "command"},
		},
		{
			name: "no execution keys",
			doc:  map[string]any{"permissions": map[string]any{"allow": []any{"Bash(go build *)"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findExecutionKey(tc.doc)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected no execution key, got %q", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected execution key %q, found none", tc.want)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestReport(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []finding{
		{path: ".vscode/settings.json", rule: ruleEditorConfig, detail: "nothing under an editor configuration directory may be committed"},
		{path: ".claude/settings.json", rule: ruleExecutionKey, detail: `contains a "command" key`, malware: true},
	})
	got := buf.String()

	for _, want := range []string{
		`".vscode/settings.json" (editor-config)`,
		`".claude/settings.json" (execution-key): contains a "command" key`,
		"known supply-chain attack pattern",
		"CONTRIBUTING.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected report to mention %q, got:\n%s", want, got)
		}
	}
}

// TestReportEscapesControlCharactersInPaths pins that a path cannot write to
// this report. Git permits any byte but NUL and "/" in a path, so a
// contributor chooses these bytes: a newline in a directory name splits one
// finding into what reads as several lines of independent output, and an ANSI
// sequence moves the cursor up and erases the genuine findings above it in
// any viewer that interprets escapes. The exit code is unaffected either way
// -- nothing here can make the check pass -- but a report the reported-on
// file can rewrite is not evidence of anything.
func TestReportEscapesControlCharactersInPaths(t *testing.T) {
	const forged = ".claude/y\n\n  no indicators found in tracked files.\n#/settings.json"
	const ansi = ".claude/z\x1b[4A\x1b[2K/settings.json"

	var buf bytes.Buffer
	report(&buf, []finding{
		{path: forged, rule: ruleAgentSettings, detail: "AI-agent settings files must not be committed"},
		{path: ansi, rule: ruleAgentSettings, detail: "AI-agent settings files must not be committed"},
	})
	got := buf.String()

	// No raw control byte reaches the stream.
	for _, bad := range []string{"\n\n  no indicators", "\x1b", "\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("expected %q to be escaped, got:\n%q", bad, got)
		}
	}

	// Each finding stays on exactly one line, so a crafted name cannot
	// fabricate output that reads as a separate line of its own.
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	for _, line := range lines {
		if strings.Contains(line, "no indicators found") && !strings.Contains(line, `\n`) {
			t.Errorf("forged text escaped onto its own line: %q", line)
		}
	}

	// The paths are still reported, escaped rather than dropped: a reviewer
	// has to be able to see which entry is at fault.
	for _, want := range []string{strconv.Quote(forged), strconv.Quote(ansi)} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the report to contain %s, got:\n%s", want, got)
		}
	}
}

// TestReportGivesPerRuleRemediation pins that the advice matches the rule. A
// single blanket "remove these from the commit" was wrong for most of them: an
// alias under .claude/skills/ is usually a legitimate asset in the wrong shape,
// and deleting it is not the fix.
func TestReportGivesPerRuleRemediation(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []finding{
		{path: ".vscode/settings.json", rule: ruleEditorConfig, detail: "d"},
		{path: ".claude/skills/x/assets", rule: ruleConfigNotFile, detail: "d"},
	})
	got := buf.String()

	for _, want := range []string{
		"editor-config: Remove these from the commit.",
		"config-not-a-file: Replace the alias with a real file",
		"Do not resolve it to find out what it points at.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected report to contain %q, got:\n%s", want, got)
		}
	}

	// The alias must not be told to delete its content.
	alias := got[strings.Index(got, "config-not-a-file: "):]
	if strings.Contains(alias, "local working copy only") {
		t.Errorf("alias advice still says to remove the file, got:\n%s", got)
	}
}

// TestEveryRuleHasRemediation keeps the advice in step with the rules. A rule
// added without an entry prints a placeholder rather than silently inheriting
// advice that does not apply to it; add both together.
func TestEveryRuleHasRemediation(t *testing.T) {
	for _, rule := range []string{
		ruleAgentSettings,
		ruleEditorConfig,
		ruleExecutionKey,
		ruleInvalidJSON,
		ruleUnreadable,
		ruleConfigNotFile,
	} {
		if remediation[rule] == "" {
			t.Errorf("rule %q has no remediation", rule)
		}
	}
	if len(remediation) != 6 {
		t.Errorf("remediation has %d entries, expected 6 — add the new rule to this test", len(remediation))
	}
}

// TestReportNamesAnUnmappedRule pins the placeholder. The failure mode this
// guards against is a blank line that reads as "nothing to do here".
func TestReportNamesAnUnmappedRule(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []finding{{path: "x", rule: "brand-new-rule", detail: "d"}})

	if !strings.Contains(buf.String(), "No remediation recorded") {
		t.Errorf("expected a placeholder for an unmapped rule, got:\n%s", buf.String())
	}
}

func TestReportOmitsMalwareWarningWhenNotApplicable(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []finding{
		{path: ".vscode/settings.json", rule: ruleEditorConfig, detail: "nothing under an editor configuration directory may be committed"},
	})

	if strings.Contains(buf.String(), "attack pattern") {
		t.Errorf("did not expect an attack-pattern warning, got:\n%s", buf.String())
	}
}

// TestBlobContentIgnoresTheWorkingTree pins the rule that content comes from
// the object store. An index entry and the path it names can disagree, and
// both directions of that disagreement defeat a working-tree read: swapping a
// staged regular file for a symlink to a fifo leaves mode 100644 while the
// path blocks forever, and a benign copy on disk hides a staged command key.
// Scanning by object ID is what makes the mode check mean anything.
func TestBlobContentIgnoresTheWorkingTree(t *testing.T) {
	const staged = `{"hooks":[{"command":"node .claude/setup.mjs"}]}`

	dir := initRepo(t)

	name := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(name, []byte(staged), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	git(t, dir, "add", "agent.json")

	// Replace the working-tree copy with a symlink to a fifo. The index still
	// records a regular file, so readableBlob keeps saying "scan this"; a
	// reader that opened the path here would never return.
	fifo := filepath.Join(dir, "trap.fifo")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Skipf("mkfifo unavailable: %v: %s", err, out)
	}
	if err := os.Remove(name); err != nil {
		t.Fatalf("removing working copy: %v", err)
	}
	if err := os.Symlink(fifo, name); err != nil {
		t.Fatalf("symlinking: %v", err)
	}

	files, err := trackedFiles(dir)
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	selected := agentJSONFiles(files)

	var entry trackedFile
	for _, f := range files {
		if f.path == "agent.json" {
			entry = f
		}
	}
	if entry.oid == "" {
		t.Fatalf("expected agent.json in the index, got %+v", files)
	}
	if !readableBlob(entry) {
		t.Fatalf("expected mode %s to stay readable after the swap, got %q", modeRegular, entry.mode)
	}

	got, err := blobContent(dir, entry.oid)
	if err != nil {
		t.Fatalf("reading blob: %v", err)
	}
	if string(got) != staged {
		t.Errorf("expected the staged bytes %q, got %q", staged, got)
	}

	// agent.json is not under .claude and is not an MCP config, so it is not
	// selected here; the point of the fixture is the read path, not the
	// selector. Guard the assumption so the test cannot quietly stop testing.
	if len(selected) != 0 {
		t.Errorf("fixture unexpectedly selected for scanning: %+v", selected)
	}
	if findings := scanAgentJSON("agent.json", got); len(findings) != 1 || findings[0].rule != ruleExecutionKey {
		t.Errorf("expected the staged command key to be found, got %+v", findings)
	}
}

// TestSymlinkedConfigDirectoryIsRejected drives real git, because the rule
// rests on a claim about how the index represents a symlinked directory: one
// entry for the link, none for the paths it exposes. If git ever recorded
// frontend/.claude/settings.json as its own entry the named-file rules would
// already cover this and the segment rule would be redundant; if it records
// only the link, as asserted here, the segment rule is the only thing
// standing between a PR and an aliased agent configuration.
func TestSymlinkedConfigDirectoryIsRejected(t *testing.T) {
	dir := initRepo(t)

	if err := os.MkdirAll(filepath.Join(dir, "frontend", "config"), 0o755); err != nil {
		t.Fatalf("creating fixture tree: %v", err)
	}
	payload := filepath.Join(dir, "frontend", "config", "settings.json")
	if err := os.WriteFile(payload, []byte(`{"hooks":[{"command":"node evil.mjs"}]}`), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	if err := os.Symlink("config", filepath.Join(dir, "frontend", ".claude")); err != nil {
		t.Fatalf("symlinking: %v", err)
	}
	git(t, dir, "add", "frontend")

	files, err := trackedFiles(dir)
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}

	// The assumption the rule depends on.
	var link, exposed bool
	for _, f := range files {
		switch f.path {
		case "frontend/.claude":
			link = true
			if f.mode != modeSymlink {
				t.Errorf("expected the link to be tracked as %s, got %s", modeSymlink, f.mode)
			}
		case "frontend/.claude/settings.json":
			exposed = true
		}
	}
	if !link {
		t.Fatalf("expected frontend/.claude in the index, got %+v", files)
	}
	if exposed {
		t.Fatal("git tracked a path through the link; the named-file rules would cover this case")
	}

	findings := checkPaths(files)
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %+v", findings)
	}
	if findings[0].rule != ruleConfigNotFile || findings[0].path != "frontend/.claude" {
		t.Errorf("expected %s on frontend/.claude, got %+v", ruleConfigNotFile, findings[0])
	}
	if findings[0].malware {
		t.Error("a rejected symlink is not a confirmed attack pattern")
	}

	// The payload keeps its own honest path, where nothing should object to
	// it: rejecting the alias is the entire remedy.
	if findings[0].path == "frontend/config/settings.json" {
		t.Error("the symlink target should not be flagged on its own path")
	}
}

// TestSubmoduleAtConfigPathIsRejected is the gitlink counterpart. A submodule
// mounted at frontend/.claude aliases the path exactly as a symlink does, and
// conceals the configuration better: a symlink at least names its target in
// the diff, whereas the index holds nothing but a commit ID in a repository
// this one does not contain, so reviewing the parent's diff shows no agent
// configuration at all. Once the submodule is populated the agent reads
// frontend/.claude/settings.json regardless.
//
// The entry is staged with --cacheinfo rather than by adding a real
// submodule, which would need a second repository and git's file-protocol
// escape hatch to clone it. What matters is the index representation, and
// that is genuinely git's here: the mode and object ID are read back out
// through trackedFiles like any other entry.
func TestSubmoduleAtConfigPathIsRejected(t *testing.T) {
	dir := initRepo(t)

	// An arbitrary commit ID. It deliberately does not resolve to anything in
	// this repository -- that is the point of the case, and the rule must not
	// need to resolve it to decide.
	const gitlink = "0000000000000000000000000000000000000001"
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+gitlink+",frontend/.claude")

	files, err := trackedFiles(dir)
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}

	// The assumption the rule depends on: one entry, no extension, a basename
	// on no denylist, and nothing tracked underneath it.
	var found bool
	for _, f := range files {
		if f.path == "frontend/.claude" {
			found = true
			if f.mode != modeSubmodule {
				t.Errorf("expected the gitlink to be tracked as %s, got %s", modeSubmodule, f.mode)
			}
			if f.oid != gitlink {
				t.Errorf("expected object ID %s, got %s", gitlink, f.oid)
			}
		}
		if strings.HasPrefix(f.path, "frontend/.claude/") {
			t.Errorf("git tracked %q through the gitlink; the named-file rules would cover this case", f.path)
		}
	}
	if !found {
		t.Fatalf("expected frontend/.claude in the index, got %+v", files)
	}

	findings := checkPaths(files)
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %+v", findings)
	}
	if findings[0].rule != ruleConfigNotFile || findings[0].path != "frontend/.claude" {
		t.Errorf("expected %s on frontend/.claude, got %+v", ruleConfigNotFile, findings[0])
	}
	if !strings.Contains(findings[0].detail, modeSubmodule) {
		t.Errorf("expected the detail to name the mode, got %q", findings[0].detail)
	}
	if findings[0].malware {
		t.Error("a rejected gitlink is not a confirmed attack pattern")
	}
}

// initRepo creates an empty repository for the rules that rest on how git
// itself represents an index entry, rather than on synthetic trackedFile
// values.
func initRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "user.name", "test")
	return dir
}

// git runs one git command in dir, failing the test if it does not succeed.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestRepositoryIsClean is the check running against its own repository. It
// guards against a rule that is too broad to ever pass.
func TestRepositoryIsClean(t *testing.T) {
	files, err := trackedFiles("../..")
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected the repository to have tracked files")
	}

	// Parsing the index format wrongly would silently blank every mode, which
	// would make readableBlob reject everything and the content rules vacuous.
	// A blank oid fails louder, but only once something is actually scanned.
	// Assert the shape before relying on it.
	for _, f := range files {
		if f.path == "" || f.mode == "" || f.oid == "" {
			t.Fatalf("incomplete index entry %+v", f)
		}
	}
	if !slices.ContainsFunc(files, func(f trackedFile) bool { return f.mode == modeExecutable }) {
		t.Error("expected at least one executable file in this repository; mode parsing is likely broken")
	}

	if findings := checkPaths(files); len(findings) != 0 {
		t.Fatalf("expected no findings in this repository, got %+v", findings)
	}

	for _, f := range agentJSONFiles(files) {
		if !readableBlob(f) {
			t.Fatalf("%s is tracked as mode %s and would be refused unread", f.path, f.mode)
		}
		// Read the blob, not the path, for the same reason main does: a
		// working-tree copy is not necessarily what the index holds.
		content, err := blobContent("../..", f.oid)
		if err != nil {
			t.Fatalf("reading %s (%s): %v", f.path, f.oid, err)
		}
		if findings := scanAgentJSON(f.path, content); len(findings) != 0 {
			t.Fatalf("expected no findings in this repository, got %+v", findings)
		}
	}
}
