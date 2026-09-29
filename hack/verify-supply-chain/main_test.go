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
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
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

		{name: "similarly named file", path: "config/mcp.json.tmpl"},
		{name: "devcontainer config", path: ".devcontainer/devcontainer.json"},
		{name: "devcontainer script", path: ".devcontainer/postCreate.sh"},
		{name: "unrelated settings file", path: "config/settings.json"},
		{name: "unrelated extensions file", path: "docs/extensions.json"},
		{name: "similarly named directory", path: "notclaude/settings.json"},
		{name: "ordinary shell script", path: "hack/verify.sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := checkPaths([]string{tc.path})

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
	findings := checkPaths([]string{
		"README.md",
		".claude/settings.json",
		"frontend/.vscode/extensions.json",
		".claude/skills/pr-standards/SKILL.md",
		".mcp.json",
	})

	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %+v", findings)
	}
}

func TestAgentJSONFiles(t *testing.T) {
	got := agentJSONFiles([]string{
		".claude/settings.json",
		".claude/skills/x/SKILL.md",
		"frontend/.claude/other.json",
		"config/config.json",
		".vscode/settings.json",
		".Claude/Payload.JSON",
		// MCP config is scanned wherever it sits, so that a command hidden in
		// a project-scoped server entry still reaches the content rules.
		".mcp.json",
		".cursor/mcp.json",
	})

	want := []string{
		".claude/settings.json",
		"frontend/.claude/other.json",
		".Claude/Payload.JSON",
		".mcp.json",
		".cursor/mcp.json",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: expected %q, got %q", i, want[i], got[i])
		}
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
		{path: ".vscode/settings.json", rule: ruleEditorConfig, detail: "editor configuration files must not be committed"},
		{path: ".claude/settings.json", rule: ruleExecutionKey, detail: `contains a "command" key`, malware: true},
	})
	got := buf.String()

	for _, want := range []string{
		".vscode/settings.json (editor-config)",
		`.claude/settings.json (execution-key): contains a "command" key`,
		"known supply-chain attack pattern",
		"CONTRIBUTING.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected report to mention %q, got:\n%s", want, got)
		}
	}
}

func TestReportOmitsMalwareWarningWhenNotApplicable(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []finding{
		{path: ".vscode/settings.json", rule: ruleEditorConfig, detail: "editor configuration files must not be committed"},
	})

	if strings.Contains(buf.String(), "attack pattern") {
		t.Errorf("did not expect an attack-pattern warning, got:\n%s", buf.String())
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

	if findings := checkPaths(files); len(findings) != 0 {
		t.Fatalf("expected no findings in this repository, got %+v", findings)
	}

	for _, rel := range agentJSONFiles(files) {
		content, err := os.ReadFile(filepath.Join("../..", rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if findings := scanAgentJSON(rel, content); len(findings) != 0 {
			t.Fatalf("expected no findings in this repository, got %+v", findings)
		}
	}
}
