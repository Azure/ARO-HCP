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

package dependabot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSDKMetadataConstraints(t *testing.T) {
	filename := "test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/go.mod"
	before := "module github.com/Azure/ARO-HCP/" + path.Dir(filename) + "\n\ngo 1.25.0\n\nrequire github.com/Azure/azure-sdk-for-go/sdk/azcore v1.23.0\n"
	after := strings.ReplaceAll(strings.ReplaceAll(before, "go 1.25.0", "go 1.26.0"), "v1.23.0", "v1.23.3") + "\nrequire (\n golang.org/x/net v0.59.0 // indirect\n golang.org/x/text v0.42.0 // indirect\n)\n"
	if err := nativeValidateSDKModule(filename, before, after, "1.26.0"); err != nil {
		t.Fatal(err)
	}
	if !nativeSDKConsumer.MatchString(filename) || nativeSDKConsumer.MatchString("test/sdk/arbitrary/go.mod") {
		t.Fatal("invalid SDK boundary")
	}
	for _, unsafe := range []string{after + "\nreplace example.com/package => /tmp/untrusted\n", after + "\ntoolchain go1.27.0\n", after + "\nrequire attacker.example/unrelated v1.0.0\n", strings.ReplaceAll(after, "go 1.26.0", "go 1.27.0"), strings.ReplaceAll(after, "module github.com/Azure/ARO-HCP/", "module attacker.example/"), strings.ReplaceAll(after, "v1.23.3", "v1.22.0")} {
		if err := nativeValidateSDKModule(filename, before, unsafe, "1.26.0"); err == nil {
			t.Errorf("accepted unsafe SDK metadata %s", unsafe)
		}
	}
}

func TestNativeNPMOverrideScopesAndResolutionPreservation(t *testing.T) {
	resolved := map[string]nativeResolved{"node_modules/parent": {Package: "parent", Version: "1.2.0"}, "node_modules/parent/node_modules/child": {Package: "child", Version: "2.3.4"}, "node_modules/child": {Package: "child", Version: "1.0.0"}}
	manifest := `{"overrides":{"parent@^1.0.0":{"child":"^2.3.0"}}}`
	if err := nativeValidateNPMOverrides(manifest, resolved); err != nil {
		t.Fatal(err)
	}
	resolved["node_modules/parent/node_modules/child"] = nativeResolved{Package: "child", Version: "2.2.0"}
	if err := nativeValidateNPMOverrides(manifest, resolved); err == nil {
		t.Fatal("accepted scoped downgrade")
	}
	if err := nativeValidateNPMOverrides(`{"dependencies":{"child":"^2.0.0"},"overrides":{"child":"$child"}}`, resolved); err == nil {
		t.Fatal("accepted reference override violation")
	}
	if err := nativeValidateNPMOverrides(`{"overrides":{"child":"$missing"}}`, resolved); err == nil {
		t.Fatal("accepted missing reference")
	}
	old := map[string]nativeResolved{"node_modules/child": {Package: "child", Version: "2.0.0"}}
	if err := nativePreserveNPMResolutions(old, resolved); err == nil {
		t.Fatal("accepted existing resolution downgrade")
	}
	if _, _, err := nativeOverrideMatches("child@latest", "1.0.0"); err == nil {
		t.Fatal("accepted unsupported selector")
	}
}

func TestNativeTreeCoverageModesAndProtectedPaths(t *testing.T) {
	root := t.TempDir()
	nativeRequestGit(t, root, "init", "-q")
	before := "module example.com/test\n\ngo 1.26.0\n\nrequire example.com/family v1.0.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRequestGit(t, root, "add", ".")
	nativeRequestGit(t, root, "commit", "-qm", "base")
	base := nativeRequestGit(t, root, "rev-parse", "HEAD")
	after := strings.ReplaceAll(before, "v1.0.0", "v1.1.0")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRequestGit(t, root, "add", ".")
	tree := nativeRequestGit(t, root, "write-tree")
	intents := []any{map[string]any{"ecosystem": "go", "package": "example.com/family", "manifest": "go.mod", "key": "example.com/family", "target": "v1.2.0"}}
	group := map[string]any{"ecosystem": "go", "family": "example.com/family", "sources": []any{map[string]any{"intents": intents}}}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, []string{"go.mod"}, group, false); err != nil {
		t.Fatal(err)
	}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, []string{"go.mod"}, group, true); err == nil {
		t.Fatal("complete coverage accepted older intent")
	}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, []string{"Makefile"}, group, false); err == nil {
		t.Fatal("accepted protected path")
	}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, []string{}, group, false); err == nil {
		t.Fatal("accepted incomplete path list")
	}
}

func TestNativeActionCoverageOfficialSourceAndCompiledPins(t *testing.T) {
	sha := strings.Repeat("a", 40)
	documents := map[string]string{".github/workflows/check.md": "steps:\n  - uses: actions/setup-go@v6\n", ".github/workflows/check.lock.yml": "steps:\n  - uses: actions/setup-go@" + sha + "\n", ".github/aw/actions-lock.json": `{"entries":{"actions/setup-go@v6":{"repo":"actions/setup-go","version":"v6","sha":"` + sha + `"}}}`}
	available := map[string]bool{}
	for filename := range documents {
		available[filename] = true
	}
	reader := func(filename string) (string, error) {
		value, ok := documents[filename]
		if !ok {
			return "", fmt.Errorf("missing %s", filename)
		}
		return value, nil
	}
	read := func(ctx context.Context, endpoint string, paginated, ci bool) (json.RawMessage, error) {
		if endpoint != "repos/actions/setup-go/commits/v6" || paginated || ci {
			return nil, fmt.Errorf("unexpected %s", endpoint)
		}
		return json.RawMessage(`{"sha":"` + sha + `"}`), nil
	}
	intent := map[string]any{"ecosystem": "actions", "package": "actions/setup-go", "target": sha, "manifest": ".github/workflows/check.md"}
	if err := NativeVerifyCoverage(context.Background(), read, []map[string]any{intent}, reader, available); err != nil {
		t.Fatal(err)
	}
	delete(available, ".github/workflows/check.lock.yml")
	if err := NativeVerifyCoverage(context.Background(), read, []map[string]any{intent}, reader, available); err == nil {
		t.Fatal("accepted missing compiled consumer")
	}
	paths := NativeCoveragePaths([]map[string]any{intent})
	if !paths[".github/workflows/check.lock.yml"] || !paths[".github/aw/actions-lock.json"] {
		t.Fatal("coverage evidence paths missing")
	}
}

func TestNativeVersionCompatibility(t *testing.T) {
	for _, row := range []struct {
		actual, target string
		resolved, want bool
	}{{"1.2.3", "^1.2.0", true, true}, {"2.0.0", "^1.2.0", true, false}, {"0.3.0", "^0.2.0", true, false}, {"1.3.0", "~1.2.0", true, false}, {"~1.2.3", "^1.2.0", false, true}, {"^1.2.3", "~1.2.0", false, false}} {
		if actual := nativeNPMCovers(row.actual, row.target, row.resolved); actual != row.want {
			t.Errorf("%+v got %v", row, actual)
		}
	}
	if nativeVersionCovers("v1.2", "v1.0.0") || nativeVersionCovers("v1.0.0-01", "v1.0.0-alpha") || !nativeVersionCovers("v1.2.3", "v1.2.3-rc.1") {
		t.Fatal("Go semver policy mismatch")
	}
}

func TestNativeTreeRejectsRevisionOptionsBeforeGitWrites(t *testing.T) {
	root := t.TempDir()
	nativeRequestGit(t, root, "init", "-q")
	filename := filepath.Join(root, "go.mod")
	if err := os.WriteFile(filename, []byte("module example.com/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRequestGit(t, root, "add", ".")
	nativeRequestGit(t, root, "commit", "-qm", "base")
	base := nativeRequestGit(t, root, "rev-parse", "HEAD")
	output := filepath.Join(t.TempDir(), "must-not-be-created")
	group := map[string]any{"ecosystem": "go", "family": "example.com/test"}
	for _, revisions := range [][2]string{
		{"--output=" + output, base},
		{base, "--output=" + output},
		{"--ext-diff", base},
		{base, strings.ToUpper(base)},
		{base[:12], base},
	} {
		err := NativeValidateTree(context.Background(), nil, root, revisions[0], revisions[1], []string{"go.mod"}, group, false)
		if _, ok := err.(*NativePolicyError); !ok {
			t.Fatalf("expected ordinary policy rejection for %v, got %v", revisions, err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("revision option created output: %v", err)
		}
	}
	if err := NativeValidateTree(context.Background(), nil, "", base, base, nil, group, false); err == nil {
		t.Fatal("accepted empty repository root")
	}
}
