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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeChangesRejectsManifestBehaviorChanges(t *testing.T) {
	cases := []struct {
		filename string
		before   string
		after    string
	}{
		{
			filename: "go.mod",
			before:   "module old\nrequire example.org/mod v1.0.0\n",
			after:    "module evil\nrequire example.org/mod v1.1.0\n",
		},
		{
			filename: "package.json",
			before:   `{"dependencies":{"pkg":"1"},"scripts":{"postinstall":"safe"}}`,
			after:    `{"dependencies":{"pkg":"2"},"scripts":{"postinstall":"evil"}}`,
		},
		{
			filename: ".github/workflows/test.yml",
			before:   "steps:\n - uses: actions/checkout@v3\n   run: safe\n",
			after:    "steps:\n - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n   run: evil\n",
		},
	}
	for _, test := range cases {
		t.Run(test.filename, func(t *testing.T) {
			_, err := nativeChanges(context.Background(), nil, test.filename, test.before, test.after)
			if err == nil {
				t.Fatal("accepted behavioral change")
			}
		})
	}
}

func TestNativePolicyJSONRejectsAmbiguity(t *testing.T) {
	for _, document := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{} {}`, `null {}`} {
		if _, err := nativeDecode([]byte(document)); err == nil {
			t.Fatalf("accepted %s", document)
		}
	}
}

func TestNativeTreeRejectsPartialMetadata(t *testing.T) {
	for _, group := range []map[string]any{nil, {"ecosystem": "go", "family": "example.com/a"}, {"ecosystem": "go", "family": "example.com/a", "sources": []any{nil}}, {"ecosystem": "go", "family": "example.com/a", "sources": []any{map[string]any{"intents": []any{map[string]any{"ecosystem": "go"}}}}}} {
		if err := nativeValidateTreeMetadata(group); err == nil {
			t.Fatalf("accepted %#v", group)
		}
	}
}

func TestNativeTreeDefersStaleCompiledCoverageUntilFinal(t *testing.T) {
	root := t.TempDir()
	nativeRequestGit(t, root, "init", "-q")
	directory := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	old, target := strings.Repeat("a", 40), strings.Repeat("b", 40)
	source := "steps:\n  - uses: actions/checkout@" + old + "\n"
	for _, name := range []string{"check.md", "check.lock.yml"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	nativeRequestGit(t, root, "add", ".")
	nativeRequestGit(t, root, "commit", "-qm", "base")
	base := nativeRequestGit(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(directory, "check.md"), []byte(strings.ReplaceAll(source, old, target)), 0644); err != nil {
		t.Fatal(err)
	}
	nativeRequestGit(t, root, "add", ".")
	tree := nativeRequestGit(t, root, "write-tree")
	group := map[string]any{"ecosystem": "actions", "family": "actions/checkout", "sources": []any{map[string]any{"intents": []any{map[string]any{"ecosystem": "actions", "package": "actions/checkout", "manifest": ".github/workflows/check.md", "target": target}}}}}
	paths := []string{".github/workflows/check.md"}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, paths, group, false); err != nil {
		t.Fatal(err)
	}
	if err := NativeValidateTree(context.Background(), nil, root, base, tree, paths, group, true); err == nil {
		t.Fatal("accepted stale compiled pin")
	}
}

func TestNativeCompilerHeadersPreserveExecutableProvenance(t *testing.T) {
	pin := strings.Repeat("a", 40)
	hash := strings.Repeat("b", 64)
	metadata := `# gh-aw-metadata: {"frontmatter_hash":"` + hash + `","body_hash":"` + hash + `","secrets":["SAFE"]}`
	source := metadata + "\nsteps:\n  - uses: actions/checkout@" + pin + "\n"
	changed := strings.ReplaceAll(source, hash, strings.Repeat("c", 64))
	if _, err := nativeChanges(context.Background(), nil, ".github/workflows/check.lock.yml", source, changed); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{
		strings.ReplaceAll(changed, "SAFE", "ATTACKER"),
		strings.ReplaceAll(changed, strings.Repeat("c", 64), "partial"),
		changed + metadata + "\n",
	} {
		if _, err := nativeChanges(context.Background(), nil, ".github/workflows/check.lock.yml", source, unsafe); err == nil {
			t.Fatal("accepted altered compiler provenance")
		}
	}
}

func TestNativeNestedSelfOverrideRetainsPackageIdentity(t *testing.T) {
	versions, err := nativeManifestVersions("package.json", `{"overrides":{"@typespec/compiler@^1.0.0":{".":"^1.2.0"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		if version.Package != "@typespec/compiler" {
			t.Fatalf("lost scoped package identity: %#v", version)
		}
	}
}

func TestNativeChangesPreservesArbitraryUnchangedNPMMetadata(t *testing.T) {
	before := `{"name":"fixture","config":{"Target":"browser","Repository":"custom","number":9007199254740993},"devDependencies":{"prettier":"3.9.9"}}`
	after := strings.Replace(before, "3.9.9", "3.9.10", 1)
	changes, err := nativeChanges(context.Background(), nil, "api/package.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0]["package"] != "prettier" || changes[0]["old"] != "3.9.9" || changes[0]["target"] != "3.9.10" {
		t.Fatalf("incorrect dependency intent: %#v", changes)
	}
	if _, err := nativeChanges(context.Background(), nil, "api/package.json", before, strings.Replace(after, `"browser"`, `"server"`, 1)); err == nil {
		t.Fatal("accepted unrelated metadata mutation")
	}
}

// TestNativeCompilerActionsRequiresOfficialTags checks every changed inventory pin, including raw SHA versions.
func TestNativeCompilerActionsRequiresOfficialTags(t *testing.T) {
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	cases := []struct {
		name     string
		previous string
		version  string
		tags     string
		reject   bool
	}{
		{name: "release tag", previous: "v1", version: "v2", tags: `[{"name":"v2","commit":{"sha":"` + sha + `"}}]`},
		{name: "stale unchanged version", previous: "v2", version: "v2", tags: `[{"name":"v2","commit":{"sha":"` + other + `"}}]`, reject: true},
		{name: "raw SHA with tag", previous: "v1", version: sha, tags: `[{"name":"v2","commit":{"sha":"` + sha + `"}}]`},
		{name: "raw SHA without tag", previous: "v1", version: sha, tags: `[]`, reject: true},
		{name: "different raw SHA", previous: "v1", version: other, tags: `[{"name":"v2","commit":{"sha":"` + sha + `"}}]`, reject: true},
		{name: "unchanged raw SHA with tag", previous: sha, version: sha, tags: `[{"name":"v2","commit":{"sha":"` + sha + `"}}]`},
		{name: "unchanged raw SHA without tag", previous: sha, version: sha, tags: `[]`, reject: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			left := map[string]any{"actions": []any{map[string]any{"repo": "actions/checkout", "sha": other, "version": test.previous}}}
			right := map[string]any{"actions": []any{map[string]any{"repo": "actions/checkout", "sha": sha, "version": test.version}}}
			pins := nativeVersionSet{"pin": {Package: "actions/checkout", Version: sha}}
			read := func(_ context.Context, endpoint string, pages, ci bool) (json.RawMessage, error) {
				if endpoint != "repos/actions/checkout/tags?per_page=100&page=1" || pages || ci {
					t.Fatalf("unsupported official read %q", endpoint)
				}
				return json.RawMessage(test.tags), nil
			}
			err := nativeCompilerActions(context.Background(), read, left, right, pins)
			if (err != nil) != test.reject {
				t.Fatalf("error = %v, reject = %t", err, test.reject)
			}
		})
	}
}
