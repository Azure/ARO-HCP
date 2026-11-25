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
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"
)

// NativePublicationFixtureBlob constructs a valid Git object identity for fixture file bytes.
func nativePublicationFixtureBlob(text string) map[string]any {
	sha := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(text), text)))
	return map[string]any{"type": "file", "sha": fmt.Sprintf("%x", sha), "size": len(text), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(text))}
}

// TestNativePublicationContent checks complete small/large blob identity, size and encoding evidence.
func TestNativePublicationContent(t *testing.T) {
	sha := strings.Repeat("a", 40)
	text := "module example.test/root\n"
	tests := []struct {
		name      string
		configure func(map[string]any, *nativeIntegrationFixture)
		wantError bool
	}{
		{name: "regular file"},
		{name: "large file fallback", configure: func(file map[string]any, f *nativeIntegrationFixture) {
			blob := nativePublicationFixtureBlob(text)
			file["encoding"] = "none"
			delete(file, "content")
			f.overrides["git/blobs/"+blob["sha"].(string)] = blob
		}},
		{name: "missing size", configure: func(file map[string]any, f *nativeIntegrationFixture) { delete(file, "size") }, wantError: true},
		{name: "truncated bytes", configure: func(file map[string]any, f *nativeIntegrationFixture) { file["size"] = len(text) + 1 }, wantError: true},
		{name: "wrong blob identity", configure: func(file map[string]any, f *nativeIntegrationFixture) { file["sha"] = strings.Repeat("b", 40) }, wantError: true},
		{name: "invalid base64", configure: func(file map[string]any, f *nativeIntegrationFixture) { file["content"] = "!!!" }, wantError: true},
		{name: "symlink rejected", configure: func(file map[string]any, f *nativeIntegrationFixture) { file["type"] = "symlink" }, wantError: true},
		{name: "missing content", configure: func(file map[string]any, f *nativeIntegrationFixture) { delete(file, "content") }, wantError: true},
		{name: "large blob identity changed", configure: func(file map[string]any, f *nativeIntegrationFixture) {
			blob := nativePublicationFixtureBlob(text)
			original := blob["sha"].(string)
			file["encoding"] = "none"
			blob["sha"] = strings.Repeat("b", 40)
			f.overrides["git/blobs/"+original] = blob
		}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNativeIntegrationFixture(t)
			file := nativePublicationFixtureBlob(text)
			fixture.overrides["contents/go.mod?ref="+sha] = file
			if test.configure != nil {
				test.configure(file, fixture)
			}
			got, err := fixture.publisher.nativePublicationContent(context.Background(), sha, "go.mod")
			if (err != nil) != test.wantError {
				t.Fatalf("content error %v, want error %v", err, test.wantError)
			}
			if !test.wantError && got != text {
				t.Fatalf("content = %q, want %q", got, text)
			}
		})
	}
}

// TestNativePublicationPrimaryNPMGraph retains same-family root solver bumps but not unrelated transitives.
func TestNativePublicationPrimaryNPMGraph(t *testing.T) {
	before := `{"dependencies":{"@typespec/compiler":"0.1.0","@typespec/http":"^0.1.0","unrelated":"1.0.0"}}`
	after := `{"dependencies":{"@typespec/compiler":"0.2.0","@typespec/http":"^0.1.0","unrelated":"1.0.0"}}`
	oldLock := `{"lockfileVersion":3,"packages":{"":{},"node_modules/@typespec/compiler":{"version":"0.1.0"},"node_modules/@typespec/http":{"version":"0.1.0"},"node_modules/unrelated":{"version":"1.0.0"}}}`
	newLock := `{"lockfileVersion":3,"packages":{"":{},"node_modules/@typespec/compiler":{"version":"0.2.0"},"node_modules/@typespec/http":{"version":"0.2.0"},"node_modules/unrelated":{"version":"2.0.0"},"node_modules/new-transitive":{"version":"1.0.0"}}}`
	tests := []struct {
		name         string
		manifest     string
		lock         string
		omitManifest bool
		wantError    string
	}{
		{name: "same root graph", manifest: "api/package.json", lock: "api/package-lock.json"},
		{name: "unrelated consumer graph", manifest: "api/package.json", lock: "other/package-lock.json", wantError: "unrelated manifest consumer"},
		{name: "missing immutable root", manifest: "api/package.json", lock: "api/package-lock.json", omitManifest: true, wantError: "lacks immutable root"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher := &NativePublisher{}
			primary, err := nativeChanges(context.Background(), nil, test.manifest, before, after)
			if err != nil {
				t.Fatal(err)
			}
			locks, err := nativeChanges(context.Background(), nil, test.lock, oldLock, newLock)
			if err != nil {
				t.Fatal(err)
			}
			documents := map[string][2]string{test.manifest: {before, after}, test.lock: {oldLock, newLock}}
			if test.omitManifest {
				delete(documents, test.manifest)
			}
			pull := &github.PullRequest{Number: github.Ptr(1), Head: &github.PullRequestBranch{SHA: github.Ptr(strings.Repeat("a", 40))}}
			row, err := publisher.nativePublicationGroupIntent(context.Background(), pull, "npm", append(primary, locks...), documents)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("npm error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			intents := row["intents"].([]any)
			if len(intents) != 2 || row["target"] != "0.2.0" || row["family"] != "typespec" {
				t.Fatalf("npm intents = %#v", row)
			}
			first := intents[0].(map[string]any)
			second := intents[1].(map[string]any)
			if !reflect.DeepEqual(first["lock_manifests"], []any{test.lock}) || second["package"] != "@typespec/http" || second["direct"] != false || second["manifest"] != test.lock {
				t.Fatalf("npm consumer evidence = %#v", intents)
			}
		})
	}
}

// TestNativePublicationTargetFormatting preserves Python target identity including sorted mixed versions.
func TestNativePublicationTargetFormatting(t *testing.T) {
	tests := []struct {
		name    string
		targets map[string]any
		want    any
	}{
		{name: "single floor", targets: map[string]any{"b": "1.0", "a": "1.0"}, want: "1.0"},
		{name: "distinct floors", targets: map[string]any{"b": "2.0", "a": "1.0"}, want: `{"a": "1.0", "b": "2.0"}`},
		{name: "removed Go dependency", targets: map[string]any{"b": nil, "a": "v1.0.0"}, want: `{"a": "v1.0.0", "b": null}`},
		{name: "ASCII JSON compatibility", targets: map[string]any{"é": "<2", "z": "😀"}, want: `{"z": "\ud83d\ude00", "\u00e9": "<2"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativePublicationTarget(test.targets); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("target = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestNativePublicationOfficialTags requires semantic release evidence and validates even later tag rows.
func TestNativePublicationOfficialTags(t *testing.T) {
	pin := strings.Repeat("a", 40)
	tests := []struct {
		name      string
		tags      []any
		wantError bool
	}{
		{name: "official release", tags: []any{map[string]any{"name": "v1.2.3", "commit": map[string]any{"sha": pin}}}},
		{name: "major alias only", tags: []any{map[string]any{"name": "v1", "commit": map[string]any{"sha": pin}}}, wantError: true},
		{name: "unreleased commit", tags: []any{map[string]any{"name": "v1.2.3", "commit": map[string]any{"sha": strings.Repeat("b", 40)}}}, wantError: true},
		{name: "malformed later row", tags: []any{map[string]any{"name": "v1.2.3", "commit": map[string]any{"sha": pin}}, map[string]any{"name": "v1.2.4"}}, wantError: true},
		{name: "duplicate tag", tags: []any{map[string]any{"name": "v1.2.3", "commit": map[string]any{"sha": pin}}, map[string]any{"name": "v1.2.3", "commit": map[string]any{"sha": pin}}}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNativeIntegrationFixture(t)
			fixture.overrides["/repos/actions/checkout/tags"] = test.tags
			err := fixture.publisher.nativePublicationOfficialPin(context.Background(), "actions/checkout", pin)
			if (err != nil) != test.wantError {
				t.Fatalf("tag evidence error = %v, want error %v", err, test.wantError)
			}
		})
	}
}

// TestNativePublicationLockOnlyIntent rejects ungrounded solver additions and unrelated package families.
func TestNativePublicationLockOnlyIntent(t *testing.T) {
	tests := []struct {
		name      string
		before    string
		after     string
		wantError string
	}{
		{name: "single resolved upgrade", before: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"1.0.0"}}}`, after: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"2.0.0"}}}`},
		{name: "addition lacks primary evidence", before: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"1.0.0"}}}`, after: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"2.0.0"},"node_modules/b":{"version":"1.0.0"}}}`, wantError: "additions/removals lack primary"},
		{name: "removal lacks primary evidence", before: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"1.0.0"},"node_modules/b":{"version":"1.0.0"}}}`, after: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"2.0.0"}}}`, wantError: "additions/removals lack primary"},
		{name: "different resolved families", before: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"1.0.0"},"node_modules/b":{"version":"1.0.0"}}}`, after: `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"2.0.0"},"node_modules/b":{"version":"2.0.0"}}}`, wantError: "multiple upgrade families"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intents, err := nativeChanges(context.Background(), nil, "package-lock.json", test.before, test.after)
			if err != nil {
				t.Fatal(err)
			}
			publisher := &NativePublisher{}
			pull := &github.PullRequest{Number: github.Ptr(1), Head: &github.PullRequestBranch{SHA: github.Ptr(strings.Repeat("a", 40))}}
			row, err := publisher.nativePublicationGroupIntent(context.Background(), pull, "npm", intents, nil)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("lock-only intent error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil || row["family"] != "a" || row["target"] != "2.0.0" {
				t.Fatalf("lock-only intent = %#v, error %v", row, err)
			}
		})
	}
}

// TestNativePublicationReadBoundary keeps publication credentials behind the fixed official resolver.
func TestNativePublicationReadBoundary(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		pages     bool
		ci        bool
		wantError bool
	}{
		{name: "official release", endpoint: "repos/actions/checkout/tags?per_page=100&page=1"},
		{name: "content cannot use resolver", endpoint: "repos/Azure/ARO-HCP/contents/go.mod", wantError: true},
		{name: "unbounded tags", endpoint: "repos/actions/checkout/tags", wantError: true},
		{name: "query injection", endpoint: "repos/actions/checkout/tags?per_page=100&page=1&ref=main", wantError: true},
		{name: "automatic pagination prohibited", endpoint: "repos/actions/checkout/tags?per_page=100&page=1", pages: true, wantError: true},
		{name: "CI credential prohibited", endpoint: "repos/actions/checkout/tags?per_page=100&page=1", ci: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNativeIntegrationFixture(t)
			fixture.overrides["/repos/actions/checkout/tags"] = []any{map[string]any{"name": "v4.0.0", "commit": map[string]any{"sha": strings.Repeat("a", 40)}}}
			raw, err := fixture.publisher.nativePublicationRead(context.Background(), test.endpoint, test.pages, test.ci)
			if (err != nil) != test.wantError {
				t.Fatalf("resolver error = %v, want error %v", err, test.wantError)
			}
			if !test.wantError && !strings.Contains(string(raw), strings.Repeat("a", 40)) {
				t.Fatalf("resolver lost immutable response: %s", raw)
			}
		})
	}
}
