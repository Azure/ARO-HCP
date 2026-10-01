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

		// A symlinked configuration directory. Git records one entry for the
		// link itself, with no extension and a basename on no denylist, and
		// nothing at all for the paths it exposes -- "frontend/.claude ->
		// config" means frontend/.claude/settings.json is never a tracked
		// path, so no named-file rule can ever see it, while an agent
		// resolves exactly that path and reads whatever config/ holds. The
		// link is rejected without being followed.
		{name: "symlinked agent directory", path: "frontend/.claude", mode: modeSymlink, rule: ruleConfigSymlink},
		{name: "symlinked root agent directory", path: ".claude", mode: modeSymlink, rule: ruleConfigSymlink},
		{name: "symlinked editor directory", path: "frontend/.vscode", mode: modeSymlink, rule: ruleConfigSymlink},
		{name: "symlinked nested editor directory", path: "a/b/.vscode", mode: modeSymlink, rule: ruleConfigSymlink},
		{name: "uppercased symlinked agent directory", path: "frontend/.Claude", mode: modeSymlink, rule: ruleConfigSymlink},
		// A symlink deeper inside an agent directory aliases just as well, so
		// the rule keys off the segment rather than the final element.
		{name: "symlinked skill subdirectory", path: ".claude/skills/x", mode: modeSymlink, rule: ruleConfigSymlink},
		{name: "symlinked file in agent directory", path: ".claude/skills/x/meta.json", mode: modeSymlink, rule: ruleConfigSymlink},
		// Named-file rules are more specific, so they keep their wording when
		// both could apply.
		{name: "symlinked agent settings", path: ".claude/settings.json", mode: modeSymlink, rule: ruleAgentSettings},
		{name: "symlinked project-scoped mcp config", path: ".mcp.json", mode: modeSymlink, rule: ruleAgentSettings},
		// Ordinary skill assets are regular files and stay unaffected; a
		// symlink elsewhere in the tree is none of this check's business.
		{name: "symlink outside config directories", path: "docs/latest", mode: modeSymlink},
		{name: "symlink in similarly named directory", path: "notclaude/link", mode: modeSymlink},

		{name: "similarly named file", path: "config/mcp.json.tmpl"},
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
		".claude/skills/x/SKILL.md",
		"frontend/.claude/other.json",
		"config/config.json",
		".vscode/settings.json",
		".Claude/Payload.JSON",
		// MCP config is scanned wherever it sits, so that a command hidden in
		// a project-scoped server entry still reaches the content rules.
		".mcp.json",
		".cursor/mcp.json",
	),
		// Selected despite not being readable. A symlink is how an execution
		// key would otherwise walk straight through this rule: git stores a
		// target path, but an agent resolves the link and reads the keys on
		// the other end. Selecting it here is what lets the caller refuse it;
		// dropping it at selection time is the bypass.
		trackedFile{path: ".claude/skills/x/meta.json", mode: "120000"},
		trackedFile{path: "frontend/.mcp.json", mode: "120000"},
		trackedFile{path: ".claude/vendor.json", mode: "160000"},
		// An executable regular file is still a plain blob and safe to read.
		trackedFile{path: ".claude/exec.json", mode: modeExecutable},
	))

	want := []string{
		".claude/settings.json",
		"frontend/.claude/other.json",
		".Claude/Payload.JSON",
		".mcp.json",
		".cursor/mcp.json",
		".claude/skills/x/meta.json",
		"frontend/.mcp.json",
		".claude/vendor.json",
		".claude/exec.json",
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
		{mode: "120000"}, // symlink
		{mode: "160000"}, // submodule
		{mode: ""},       // an index entry that failed to parse
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
	got := unreadableAgentJSON(trackedFile{path: ".claude/skills/x/meta.json", mode: "120000"})

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

// TestBlobContentIgnoresTheWorkingTree pins the rule that content comes from
// the object store. An index entry and the path it names can disagree, and
// both directions of that disagreement defeat a working-tree read: swapping a
// staged regular file for a symlink to a fifo leaves mode 100644 while the
// path blocks forever, and a benign copy on disk hides a staged command key.
// Scanning by object ID is what makes the mode check mean anything.
func TestBlobContentIgnoresTheWorkingTree(t *testing.T) {
	const staged = `{"hooks":[{"command":"node .claude/setup.mjs"}]}`

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	name := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(name, []byte(staged), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	add := exec.Command("git", "add", "agent.json")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

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
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

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
	add := exec.Command("git", "add", "frontend")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

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
	if findings[0].rule != ruleConfigSymlink || findings[0].path != "frontend/.claude" {
		t.Errorf("expected %s on frontend/.claude, got %+v", ruleConfigSymlink, findings[0])
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
