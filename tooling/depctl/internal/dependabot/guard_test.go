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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGroupDerivesTitleAndValidatesManifests(t *testing.T) {
	item := map[string]any{
		"type": "create_pull_request",
		"data": map[string]any{
			"ecosystem": "go", "package_family": "github.com/example", "target_version": "v1.2.3",
			"manifests": []any{"tooling/example/go.mod"},
		},
	}
	candidate, err := Group(item)
	if err != nil {
		t.Fatalf("Group() error = %v", err)
	}
	if candidate.Title != "github.com/example to v1.2.3" || candidate.Family != "github.com/example" {
		t.Fatalf("candidate = %+v", candidate)
	}
	item["data"].(map[string]any)["manifests"] = []any{"tooling/other/go.mod"}
	item["files"] = []any{map[string]any{"path": "tooling/example/go.mod"}}
	if _, err := Group(item); !errors.Is(err, ErrGuard) {
		t.Fatalf("Group() error = %v, want ErrGuard", err)
	}
}

func TestFamilyMatchesUsesPrefixesAndGlobs(t *testing.T) {
	cases := []struct {
		family, pkg string
		want        bool
	}{
		{"github.com/docker/distribution", "github.com/docker/distribution/registry/client/auth", true},
		{"github.com/google/go-containerregistry", "github.com/google/go-containerregistry/pkg/v1/remote", true},
		{"golang.org/x/*", "golang.org/x/net", true},
		{"undici", "@undici/types", true},
		{"undici", "undici-types", false},
	}
	for _, test := range cases {
		if got := FamilyMatches(test.family, test.pkg); got != test.want {
			t.Errorf("FamilyMatches(%q, %q) = %v, want %v", test.family, test.pkg, got, test.want)
		}
	}
}

const goPatch = `@@ -1,4 +1,4 @@
 require (
 	github.com/example/a v1.0.0
-	github.com/example/b v1.0.0
+	github.com/example/b v1.1.0
 )`

func TestPatchHunksAndGoDependencies(t *testing.T) {
	file := map[string]any{
		"filename": "tooling/example/go.mod", "status": "modified", "patch": goPatch,
		"additions": float64(1), "deletions": float64(1),
	}
	hunks, err := PatchHunks(file)
	if err != nil {
		t.Fatalf("PatchHunks() error = %v", err)
	}
	packages, err := GoDependencies(hunks)
	if err != nil {
		t.Fatalf("GoDependencies() error = %v", err)
	}
	if !packages["github.com/example/b"] || packages["github.com/example/a"] {
		t.Fatalf("packages = %v", packages)
	}
	file["patch"] = strings.Replace(goPatch, "+	github.com/example/b v1.1.0\n", "", 1)
	if _, err := PatchHunks(file); err == nil {
		t.Fatal("PatchHunks() accepted a truncated patch")
	}
}

func TestNpmDependenciesDetectsChangedEntries(t *testing.T) {
	file := map[string]any{
		"filename": "api/package.json", "status": "modified",
		"patch": `@@ -1,7 +1,7 @@
 {
   "dependencies": {
     "undici": "7.0.0",
-    "fast-uri": "3.0.0",
+    "fast-uri": "3.1.0",
     "other": "1.0.0"
   }
 }`,
	}
	hunks, err := PatchHunks(file)
	if err != nil {
		t.Fatalf("PatchHunks() error = %v", err)
	}
	packages, err := NpmDependencies(hunks)
	if err != nil {
		t.Fatalf("NpmDependencies() error = %v", err)
	}
	if !packages["fast-uri"] || packages["undici"] {
		t.Fatalf("packages = %v", packages)
	}
}

func TestLockVersionsReadsV2AndDefersLinks(t *testing.T) {
	document := map[string]any{
		"lockfileVersion": float64(2),
		"packages": map[string]any{
			"node_modules/fast-uri": map[string]any{"version": "3.0.0", "resolved": "r", "integrity": "i"},
		},
	}
	versions, err := LockVersions(document)
	if err != nil {
		t.Fatalf("LockVersions() error = %v", err)
	}
	if versions["node_modules/fast-uri"].Version != "3.0.0" {
		t.Fatalf("versions = %+v", versions)
	}
	document["packages"] = map[string]any{
		"node_modules/linked": map[string]any{"version": "1.0.0", "link": true},
	}
	if _, err := LockVersions(document); err == nil {
		t.Fatal("LockVersions() accepted a workspace link")
	}
}

func TestGuardCreateDefersOverlapAndDropsMissingData(t *testing.T) {
	output := map[string]any{"items": []any{
		map[string]any{"type": "create_pull_request", "title": "no data"},
		map[string]any{
			"type": "create_pull_request",
			"data": map[string]any{
				"ecosystem": "go", "package_family": "github.com/example/b", "target_version": "v1.2.0",
				"manifests": []any{"tooling/example/go.mod"},
			},
		},
	}}
	raw := func(_ context.Context, endpoint string) ([]byte, error) {
		switch {
		case strings.Contains(endpoint, "/files"):
			return json.Marshal([]map[string]any{{
				"filename": "tooling/example/go.mod", "status": "modified", "patch": goPatch,
				"additions": 1, "deletions": 1,
			}})
		default:
			return nil, fmt.Errorf("unexpected endpoint %s", endpoint)
		}
	}
	var warnings []string
	if err := GuardCreate(context.Background(), "owner/repo", output, []int{7}, raw,
		func(message string) { warnings = append(warnings, message) }); err != nil {
		t.Fatalf("GuardCreate() error = %v", err)
	}
	items, _ := output["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["type"] != "noop" {
		t.Fatalf("items = %+v", items)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestGuardRetestRequiresCurrentFailureAndComparison(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	raw := func(_ context.Context, endpoint string) ([]byte, error) {
		switch {
		case strings.Contains(endpoint, "/statuses"):
			return json.Marshal([]Status{{Context: "ci/prow/lint", State: "failure",
				TargetURL: "https://prow.example/lint", CreatedAt: "2026-11-16T10:00:00Z"}})
		case strings.Contains(endpoint, "/check-runs"):
			return []byte(`{"check_runs":[]}`), nil
		case strings.Contains(endpoint, "/comments"):
			return []byte(`[]`), nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %s", endpoint)
		}
	}
	evidence := "failed https://prow.example/lint compared with https://prow.example/green"
	if err := GuardRetest(context.Background(), "owner/repo", 7, "deadbeef", evidence, raw, now); err != nil {
		t.Fatalf("GuardRetest() error = %v", err)
	}
	if err := GuardRetest(context.Background(), "owner/repo", 7, "deadbeef",
		"only https://prow.example/lint", raw, now); !errors.Is(err, ErrGuard) {
		t.Fatalf("GuardRetest() error = %v, want ErrGuard", err)
	}
}
