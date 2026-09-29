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
		{name: "shell script in agent dir", path: ".claude/setup.sh", rule: ruleAgentExecutable},
		{name: "node script in agent dir", path: ".claude/skills/x/setup.mjs", rule: ruleAgentExecutable},
		{name: "python script in agent dir", path: "backend/.claude/hook.py", rule: ruleAgentExecutable},

		// Git's index is case-sensitive, so each of these is a distinct
		// tracked path that must not be able to sidestep the denylist.
		{name: "uppercased script extension", path: ".claude/setup.SH", rule: ruleAgentExecutable},
		{name: "mixed-case script extension", path: ".claude/hook.Ps1", rule: ruleAgentExecutable},
		{name: "uppercased settings basename", path: ".claude/Settings.json", rule: ruleAgentSettings},
		{name: "uppercased agent directory", path: ".Claude/settings.json", rule: ruleAgentSettings},
		{name: "uppercased editor directory", path: ".VSCode/extensions.json", rule: ruleEditorConfig},
		{name: "fully uppercased path", path: "FRONTEND/.CLAUDE/SETTINGS.JSON", rule: ruleAgentSettings},

		{name: "checked-in skill", path: ".claude/skills/pr-standards/SKILL.md"},
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
		"backend/.claude/payload.mjs",
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
	})

	want := []string{".claude/settings.json", "frontend/.claude/other.json", ".Claude/Payload.JSON"}
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
		name    string
		file    string
		wantHit bool
	}{
		{name: "hook with command key", file: "settings-with-hook-command.json", wantHit: true},
		{name: "malformed json with command key", file: "settings-malformed.json", wantHit: true},
		{name: "permissions only", file: "settings-benign.json"},
		// The raw-text fallback must agree with findExecutionKey, which
		// accepts "command" in value position. Reporting this as confirmed
		// malware would be a false alarm on an ordinary hook definition that
		// merely fails to parse.
		{name: "malformed json with command only as a value", file: "settings-malformed-benign.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}

			findings := scanAgentJSON(".claude/settings.json", content)

			if !tc.wantHit {
				if len(findings) != 0 {
					t.Fatalf("expected no findings, got %+v", findings)
				}
				return
			}

			if len(findings) != 1 {
				t.Fatalf("expected exactly one finding, got %+v", findings)
			}
			if findings[0].rule != ruleExecutionKey {
				t.Errorf("expected rule %q, got %q", ruleExecutionKey, findings[0].rule)
			}
			if !findings[0].malware {
				t.Error("expected the finding to be marked as a known attack pattern")
			}
		})
	}
}

// TestScanMalformedAgentJSONMatchesKeysOnly pins the raw-text fallback to key
// position. Matching the bare token anywhere would flag benign values, and
// requiring an immediately adjacent colon would let whitespace evade it.
func TestScanMalformedAgentJSONMatchesKeysOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wantHit bool
	}{
		{name: "key", content: `{"command": "x",}`, wantHit: true},
		{name: "key with whitespace before colon", content: `{"hooks"  : [],}`, wantHit: true},
		{name: "key with newline before colon", content: "{\"command\"\n: \"x\",}", wantHit: true},
		{name: "key with a comment before its colon", content: "{\"command\" /* why */ : \"x\",}", wantHit: true},
		// Unescaped by the JSON grammar, so an escaped spelling is compared as
		// the key a consumer would actually see.
		{name: "escaped spelling of the key", content: `{"comm\u0061nd": "x",}`, wantHit: true},
		{name: "escaped quote does not end the string", content: `{"say \"command\": no": 1,}`},
		{name: "value", content: `{"type": "command",}`},
		{name: "substring of a longer key", content: `{"commands": ["x"],}`},

		// A quoted token inside a comment is prose, not a key. Documentation
		// describing the check must not be reported as an attack on it.
		{name: "key shape inside a line comment", content: "{,}\n// document the \"command\": field"},
		{name: "key shape inside a block comment", content: `{,} /* the "hooks": field */`},
		{name: "prose mentioning the key", content: `{,} // no "command" here`},

		// The mirror image, and the reason comments cannot simply be stripped
		// before matching: "//" inside a string value does not open a comment,
		// so a real key later on the same line must still be found.
		{
			name:    "url in a value does not hide a later key",
			content: `{"url": "https://example.invalid/x", "command": "bad",}`,
			wantHit: true,
		},
		{
			name:    "commented-out line does not hide a real key",
			content: "{\n// \"note\": \"x\"\n\"hooks\": [],\n}",
			wantHit: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := scanAgentJSON(".claude/settings.json", []byte(tc.content))

			if got := len(findings) > 0; got != tc.wantHit {
				t.Fatalf("expected hit=%v for %s, got %+v", tc.wantHit, tc.content, findings)
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
}
