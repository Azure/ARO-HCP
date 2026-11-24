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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

const candidateGeneratedFixture = "internal/azureapi/v20261001/generated/models.go"
const candidateSDKFixture = "test/sdk/v20990101preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"

type nativeCandidateFixture struct {
	t                               *testing.T
	v                               *nativeCandidate
	options                         NativeCandidateOptions
	base                            string
	group                           map[string]any
	commands                        []nativeCandidateCommand
	effect                          func(string, nativeCandidateCommand) error
	baselineOutput, candidateOutput string
	packageName, family, old        string
}

func candidateFixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null"}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
	content, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, content)
	}
	return strings.TrimSpace(string(content))
}

func candidateFixtureWrite(t *testing.T, root, name, text string) {
	t.Helper()
	target := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newCandidateFixture(t *testing.T) *nativeCandidateFixture {
	t.Helper()
	directory := t.TempDir()
	root := filepath.Join(directory, "candidate")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &nativeCandidateFixture{
		t:               t,
		baselineOutput:  "package generated\nconst Model = 1\n",
		candidateOutput: "package generated\nconst Model = 1\n",
		packageName:     "prettier",
		family:          "prettier",
		old:             "3.9.9",
	}
	f.options = NativeCandidateOptions{Repository: "Azure/ARO-HCP", Root: root, Inventory: filepath.Join(directory, "inventory.json"), TitleFile: filepath.Join(directory, "title.txt"), BodyFile: filepath.Join(directory, "body.txt"), Artifacts: filepath.Join(directory, "artifacts"), Target: "3.9.10"}
	candidateFixtureGit(t, root, "init", "-b", "main")
	candidateFixtureGit(t, root, "config", "user.name", "Fixture")
	candidateFixtureGit(t, root, "config", "user.email", "fixture@example.test")
	for name, text := range map[string]string{
		".gitignore":                         "node_modules\nscratch-cache\n",
		".yamllint.yml":                      "rules:\n  key-duplicates: enable\n",
		"Makefile":                           "verify-yamlfmt:\n\ttrue\nverify-vendor:\n\ttrue\n",
		"config/Makefile":                    "detect-change:\n\ttrue\n",
		candidateGeneratedFixture:            "package generated\nconst Model = 0\n",
		"go.work":                            "go 1.26.0\nuse ./internal\n",
		"internal/go.mod":                    "module github.com/Azure/ARO-HCP/internal\ngo 1.26.0\n",
		"vendor/example/THIRD PARTY NOTICES": "unchanged notices\n",
	} {
		candidateFixtureWrite(t, root, name, text)
	}
	var makefile strings.Builder
	for _, target := range nativeAPITargets {
		fmt.Fprintf(&makefile, "%s:\n\ttrue\n", target)
	}
	candidateFixtureWrite(t, root, "api/Makefile", makefile.String())
	if err := os.Symlink("THIRD PARTY NOTICES", filepath.Join(root, "vendor/example/notice-link")); err != nil {
		t.Fatal(err)
	}
	f.npm("3.9.9")
	candidateFixtureGit(t, root, "add", ".")
	candidateFixtureGit(t, root, "commit", "-m", "base")
	f.base = candidateFixtureGit(t, root, "rev-parse", "HEAD")
	f.npm(f.options.Target)
	f.observe()
	candidateFixtureWrite(t, directory, "title.txt", "chore(deps): update prettier\n")
	candidateFixtureWrite(t, directory, "body.txt", "Validate the complete native dependency group.\n")
	var err error
	f.v, err = newNativeCandidate(root, f.options.Artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.v.execute = f.consume
	return f
}

func (f *nativeCandidateFixture) npm(version string) {
	f.t.Helper()
	candidateFixtureWrite(f.t, f.options.Root, "api/package.json", fmt.Sprintf("{\"devDependencies\":{%q:%q}}\n", f.packageName, version))
	candidateFixtureWrite(f.t, f.options.Root, "api/package-lock.json", fmt.Sprintf("{\"lockfileVersion\":3,\"packages\":{\"\":{\"devDependencies\":{%q:%q}},%q:{\"version\":%q,\"resolved\":\"https://registry.npmjs.org/fixture\"}}}\n", f.packageName, version, "node_modules/"+f.packageName, version))
}

func (f *nativeCandidateFixture) observe() {
	f.t.Helper()
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte("npm:"+f.family)))[:24]
	f.options.Group = identity
	f.group = map[string]any{
		"group": identity, "ecosystem": "npm", "family": f.family,
		"target": f.options.Target, "blockers": []any{},
		"next_action": map[string]any{"action": "prepare_replacement"},
		"sources": []any{map[string]any{
			"number": 42, "head_sha": strings.Repeat("a", 40), "group": identity,
			"ecosystem": "npm", "family": f.family, "target": f.options.Target,
			"intents": []any{map[string]any{
				"ecosystem": "npm", "package": f.packageName, "old": f.old,
				"target": f.options.Target, "manifest": "api/package.json",
				"direct": true, "key": []any{"devDependencies", f.packageName},
				"lock_manifests": []any{"api/package-lock.json"},
			}},
		}},
	}
	content, err := json.Marshal(map[string]any{"repository": f.options.Repository, "base_sha": f.base, "groups": []any{f.group}})
	if err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(f.options.Inventory, content, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *nativeCandidateFixture) consume(_ context.Context, root string, command nativeCandidateCommand, logs string) model.ConsumerResult {
	f.commands = append(f.commands, command)
	if f.effect != nil {
		if err := f.effect(root, command); err != nil {
			return model.ConsumerResult{Status: model.ConsumerFailed, ExitCode: 17, Reason: err.Error()}
		}
	}
	if command.name == "base-api-models" {
		candidateFixtureWrite(f.t, root, candidateGeneratedFixture, f.baselineOutput)
	}
	if command.name == "npm-api-models" {
		candidateFixtureWrite(f.t, root, candidateGeneratedFixture, f.candidateOutput)
	}
	log := filepath.Join(logs, command.name+".fixture.log")
	text := "fixture consumer boundary\n"
	if strings.HasSuffix(command.name, "workspace") {
		text = `{"Use":[{"DiskPath":"./internal"}]}`
	}
	if err := os.WriteFile(log, []byte(text), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return model.ConsumerResult{Status: model.ConsumerPassed, LogPath: log}
}

func (f *nativeCandidateFixture) validate() error {
	_, err := f.v.validate(context.Background(), f.options)
	return err
}

func TestNativeCandidateBaselineAndCommittedGates(t *testing.T) {
	f := newCandidateFixture(t)
	f.effect = func(root string, command nativeCandidateCommand) error {
		if strings.HasPrefix(command.name, "common-") && candidateFixtureGit(t, root, "rev-parse", "HEAD") == f.base {
			t.Fatal("clean-tree gates used the uncommitted candidate")
		}
		return nil
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	if got := f.v.results["excluded_baseline_identical_generation"]; !reflect.DeepEqual(got, []string{candidateGeneratedFixture}) {
		t.Fatalf("excluded: %#v", got)
	}
	if status := candidateFixtureGit(t, f.options.Root, "status", "--porcelain"); status != "" {
		t.Fatal(status)
	}
	if listing := candidateFixtureGit(t, f.options.Root, "worktree", "list", "--porcelain"); strings.Contains(listing, "native-base-") {
		t.Fatal(listing)
	}
	request, err := os.ReadFile(filepath.Join(f.options.Artifacts, nativeRequestFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request), "Repository-owned candidate validation") || strings.Contains(string(request), "+const Model") {
		t.Fatal(string(request))
	}
	head := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	if current := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD"); current != head {
		t.Fatal("rerun created an unnecessary commit")
	}
}

func TestNativeCandidateRefusesUntrustedPreflightBeforeExecution(t *testing.T) {
	for _, test := range []struct{ name, file, content, want string }{
		{"recipe", "api/Makefile", "models:\n\ttrue\n", "protected or unrelated replacement path"},
		{"untracked", "inventory-scratch.json", "preserve me\n", "protected or unrelated replacement path"},
		{"generated", candidateGeneratedFixture, "package generated\nconst Model = 2\n", "protected or unrelated replacement path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCandidateFixture(t)
			candidateFixtureWrite(t, f.options.Root, test.file, test.content)
			err := f.validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error: %v", err)
			}
			if len(f.commands) != 0 {
				t.Fatal("executed consumers before preflight")
			}
			if got, _ := os.ReadFile(filepath.Join(f.options.Root, test.file)); string(got) != test.content {
				t.Fatal("failed preflight modified input")
			}
		})
	}
}

func TestNativeCandidateGeneratedDriftMustMatchBaseBytes(t *testing.T) {
	f := newCandidateFixture(t)
	f.candidateOutput = "package generated\nconst Model = 2\n"
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "protected or unrelated replacement path") {
		t.Fatalf("error: %v", err)
	}
	if head := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD"); head != f.base {
		t.Fatal("committed invalid generated scope")
	}
	content, _ := os.ReadFile(filepath.Join(f.options.Root, candidateGeneratedFixture))
	if string(content) != f.candidateOutput {
		t.Fatal("discarded candidate-specific generated bytes")
	}
}

func TestNativeCandidateConsumerFailuresAndLateWrites(t *testing.T) {
	for _, test := range []struct{ name, phase, file, content, want string }{
		{"failure", "npm-api-swagger", "", "", "consumer failed"},
		{"recipe-output", "npm-api-testsdk", "api/Makefile", "models:\n\ttrue\n", "protected or unrelated replacement path"},
		{"untracked-output", "npm-api-testsdk", "unexpected.txt", "preserve\n", "protected or unrelated replacement path"},
		{"dirty-gate", "common-1", "unexpected.txt", "preserve\n", "left candidate dirty"},
		{"ignored-vendor", "common-1", "vendor/example/new.go", "package example\n", "left candidate dirty"},
		{"body-tamper", "common-2", "", "", "publication text changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCandidateFixture(t)
			candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/vendor/\n")
			f.effect = func(root string, command nativeCandidateCommand) error {
				if command.name != test.phase {
					return nil
				}
				if test.name == "failure" {
					return fmt.Errorf("consumer failed")
				}
				if test.name == "body-tamper" {
					return os.WriteFile(f.options.BodyFile, []byte("tampered\n"), 0o600)
				}
				candidateFixtureWrite(t, root, test.file, test.content)
				return nil
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.options.Artifacts, nativeRequestFile)); !os.IsNotExist(err) {
				t.Fatal("failure left a request")
			}
		})
	}
}

func TestNativeCandidatePartialIndexIsPreserved(t *testing.T) {
	f := newCandidateFixture(t)
	candidateFixtureGit(t, f.options.Root, "add", "api/package.json")
	before := candidateFixtureGit(t, f.options.Root, "show", ":api/package.json")
	candidateFixtureWrite(t, f.options.Root, "api/package.json", "{\"devDependencies\":{\"prettier\":\"3.9.11\"}}\n")
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "partially staged") {
		t.Fatalf("error: %v", err)
	}
	if after := candidateFixtureGit(t, f.options.Root, "show", ":api/package.json"); after != before {
		t.Fatal("overwrote caller index")
	}
}

func TestNativeCandidateIgnoredVendorScopeAndState(t *testing.T) {
	f := newCandidateFixture(t)
	candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/vendor/\n")
	candidateFixtureWrite(t, f.options.Root, "vendor/example/new.go", "package example\n")
	candidateFixtureWrite(t, f.options.Root, "scratch-cache/ignored.txt", "scratch\n")
	state, err := f.v.state(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state["vendor/example/new.go"] == nil || state["scratch-cache/ignored.txt"] != nil {
		t.Fatal(state)
	}
	if state["vendor/example/notice-link"].Kind != "symlink" {
		t.Fatal("unchanged symlink was not preserved")
	}
	if err := f.v.stage(context.Background(), f.base, ""); err != nil {
		t.Fatal(err)
	}
	if got := candidateFixtureGit(t, f.options.Root, "show", ":vendor/example/new.go"); got != "package example" {
		t.Fatal(got)
	}
	if got := candidateFixtureGit(t, f.options.Root, "ls-files", "--", "scratch-cache"); got != "" {
		t.Fatal(got)
	}
}

func TestNativeCandidateStrictOutputRejectsLinks(t *testing.T) {
	root := t.TempDir()
	candidateFixtureWrite(t, root, "regular", "bytes\n")
	if err := os.Link(filepath.Join(root, "regular"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular", filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hardlink", "symlink"} {
		if _, err := nativeCandidateFileState(root, name, true); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestNativeCandidateEvidenceBoundsAndCompleteness(t *testing.T) {
	f := newCandidateFixture(t)
	if _, err := f.v.body(f.options.BodyFile, f.base, f.base); err == nil {
		t.Fatal("accepted no consumers")
	}
	f.v.steps = []map[string]any{{"name": "make", "status": "failed", "returncode": 0}}
	if _, err := f.v.body(f.options.BodyFile, f.base, f.base); err == nil {
		t.Fatal("accepted failed consumers")
	}
	f.v.steps = []map[string]any{{"name": "make", "status": "passed", "returncode": 0, "argv": []string{"make", "verify"}, "environment": map[string]string{}}}
	if err := os.WriteFile(f.options.BodyFile, []byte(strings.Repeat("x", 20000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.v.body(f.options.BodyFile, f.base, f.base); err == nil {
		t.Fatal("overflowed body")
	}
}

func TestNativeCandidateArtifactsAreOutsideAndAttemptsIsolated(t *testing.T) {
	f := newCandidateFixture(t)
	if _, err := newNativeCandidate(f.options.Root, filepath.Join(f.options.Root, "evidence"), nil); err == nil {
		t.Fatal("accepted artifacts in checkout")
	}
	candidateFixtureWrite(t, f.options.Artifacts, "keep.log", "old evidence\n")
	candidateFixtureWrite(t, f.options.Artifacts, nativeRequestFile, "stale request\n")
	before, err := os.ReadFile(filepath.Join(f.v.attempt, nativeCandidateResults))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newNativeCandidate(f.options.Root, f.options.Artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.attempt == f.v.attempt {
		t.Fatal("reused attempt directory")
	}
	after, _ := os.ReadFile(filepath.Join(f.v.attempt, nativeCandidateResults))
	if string(before) != string(after) {
		t.Fatal("overwrote prior evidence")
	}
	if _, err := os.Stat(filepath.Join(f.options.Artifacts, "keep.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.options.Artifacts, nativeRequestFile)); !os.IsNotExist(err) {
		t.Fatal("stale request survived")
	}
}

func TestNativeCandidateFixedMakeTargetDiscovery(t *testing.T) {
	targets := nativeCandidateTargets("fmt lint: deps\n\tmodels:\nname := value\n%.go: %.tsp\n# testsdk:\n")
	if !targets["fmt"] || !targets["lint"] || targets["models"] || targets["name"] || targets["testsdk"] {
		t.Fatal(targets)
	}
}

func TestNativeCandidateGoPlanRequiresEveryFixedTarget(t *testing.T) {
	for _, missing := range []string{"all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify"} {
		t.Run(missing, func(t *testing.T) {
			f := newCandidateFixture(t)
			var makefile strings.Builder
			for _, target := range []string{"all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify"} {
				if target != missing {
					fmt.Fprintf(&makefile, "%s:\n\ttrue\n", target)
				}
			}
			candidateFixtureWrite(t, f.options.Root, "Makefile", makefile.String())
			candidateFixtureWrite(t, f.options.Root, "image-sync/oc-mirror/acrauth/go.mod", "module example.com/acrauth\n")
			candidateFixtureGit(t, f.options.Root, "add", ".")
			candidateFixtureGit(t, f.options.Root, "commit", "-m", "Go consumer base")
			base := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
			_, err := f.v.plan(context.Background(), base, map[string]any{"ecosystem": "go"}, []string{"internal/go.mod"}, "")
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestNativeCandidateNPMPlanRequiresEveryAPITarget(t *testing.T) {
	for _, missing := range nativeAPITargets {
		t.Run(missing, func(t *testing.T) {
			f := newCandidateFixture(t)
			var makefile strings.Builder
			for _, target := range nativeAPITargets {
				if target != missing {
					fmt.Fprintf(&makefile, "%s:\n\ttrue\n", target)
				}
			}
			candidateFixtureWrite(t, f.options.Root, "api/Makefile", makefile.String())
			candidateFixtureGit(t, f.options.Root, "add", ".")
			candidateFixtureGit(t, f.options.Root, "commit", "-m", "unsupported consumer")
			base := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
			_, err := f.v.plan(context.Background(), base, map[string]any{"ecosystem": "npm"}, []string{"api/package.json"}, "")
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestNativeCandidateActionsPlanCompilesAllLiveSourcesForLock(t *testing.T) {
	f := newCandidateFixture(t)
	for _, name := range []string{"first", "second"} {
		candidateFixtureWrite(t, f.options.Root, ".github/workflows/"+name+".md", "name: agent\n")
		candidateFixtureWrite(t, f.options.Root, ".github/workflows/"+name+".lock.yml", "name: generated\n")
	}
	candidateFixtureWrite(t, f.options.Root, ".github/workflows/ordinary.yml", "name: ordinary\n")
	candidateFixtureWrite(t, f.options.Root, ".github/aw/actions-lock.json", "{}\n")
	candidateFixtureGit(t, f.options.Root, "add", ".")
	candidateFixtureGit(t, f.options.Root, "commit", "-m", "Actions base")
	base := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
	compiler, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.v.plan(context.Background(), base, map[string]any{"ecosystem": "actions"}, []string{".github/aw/actions-lock.json", ".github/workflows/ordinary.yml"}, compiler)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || plan[0].name != "actions-first" || plan[1].name != "actions-second" || plan[2].name != "actions-yaml-lint" {
		t.Fatal(plan)
	}
	want := []string{"yamllint", "-c", ".yamllint.yml", ".github/workflows/first.lock.yml", ".github/workflows/ordinary.yml", ".github/workflows/second.lock.yml"}
	if !reflect.DeepEqual(plan[2].argv, want) {
		t.Fatal(plan[2].argv)
	}
	if _, err := f.v.plan(context.Background(), base, map[string]any{"ecosystem": "actions"}, []string{".github/workflows/ordinary.yml"}, ""); err == nil {
		t.Fatal("accepted Actions without pinned compiler")
	}
}

func TestNativeCandidateSDKLicenseStillChecksRootVendor(t *testing.T) {
	f := newCandidateFixture(t)
	var names []string
	f.effect = func(_ string, command nativeCandidateCommand) error {
		names = append(names, command.name)
		if command.name == "npm-generated-verify-vendor" {
			return fmt.Errorf("generated SDK vendor is stale")
		}
		return nil
	}
	err := f.v.generatedGo(context.Background(), []string{candidateSDKFixture + "/LICENSE"}, f.base)
	if err == nil || !strings.Contains(err.Error(), "vendor is stale") {
		t.Fatalf("error: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"npm-generated-verify-vendor"}) {
		t.Fatal(names)
	}
}

func TestNativeCandidateWorkspaceRejectsUnsafeOrEmptyUse(t *testing.T) {
	for _, text := range []string{`{}`, `{"Use":[]}`, `{"Use":[{"DiskPath":"../outside"}]}`, `{"Use":[{"DiskPath":"./../outside"}]}`, `{"Use":[{"DiskPath":"./missing"}]}`} {
		t.Run(text, func(t *testing.T) {
			f := newCandidateFixture(t)
			f.v.execute = func(_ context.Context, _ string, _ nativeCandidateCommand, logs string) model.ConsumerResult {
				log := filepath.Join(logs, "workspace.log")
				if err := os.WriteFile(log, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
				return model.ConsumerResult{Status: model.ConsumerPassed, LogPath: log}
			}
			if _, err := f.v.workspace(context.Background(), "workspace"); err == nil {
				t.Fatal("accepted invalid workspace")
			}
		})
	}
}

func TestNativeCandidateRunnerRetainsFullLogsAndRejectsMakeControls(t *testing.T) {
	for _, control := range []string{"MAKEFLAGS=-n", "MFLAGS=-i", "GNUMAKEFLAGS=-q", "MAKE=true", "MAKEFILES=/absent", "MAKEOVERRIDES=MAKEFLAGS=-i", "MAKELEVEL=10"} {
		t.Run(control, func(t *testing.T) {
			root := t.TempDir()
			name, value, _ := strings.Cut(control, "=")
			t.Setenv(name, value)
			candidateFixtureWrite(t, root, "Makefile", "check:\n\t@echo executed > marker\n\t@head -c 2200000 /dev/zero\n\t@echo last-evidence\n\t@exit 37\n")
			command := nativeCandidateCommand{name: "make-consumer", argv: []string{"make", "check"}, directory: ".", environment: []string{control}}
			result := nativeCandidateRun(context.Background(), root, command, t.TempDir())
			if result.Status != model.ConsumerFailed || result.ExitCode == 0 {
				t.Fatalf("%+v", result)
			}
			log, err := os.ReadFile(result.LogPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(log) < 2200000 || !strings.Contains(string(log), "last-evidence") {
				t.Fatal("full log was truncated")
			}
			if _, err := os.Stat(filepath.Join(root, "marker")); err != nil {
				t.Fatal("inherited Make control bypassed recipe")
			}
		})
	}
}

func (f *nativeCandidateFixture) prepareGo() {
	f.t.Helper()
	root := f.options.Root
	candidateFixtureGit(f.t, root, "restore", "api/package.json", "api/package-lock.json")
	var makefile strings.Builder
	for _, name := range []string{"all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify", "verify-yamlfmt"} {
		fmt.Fprintf(&makefile, "%s:\n\ttrue\n", name)
	}
	candidateFixtureWrite(f.t, root, "Makefile", makefile.String())
	for _, directory := range []string{"internal", "image-sync/oc-mirror/acrauth"} {
		candidateFixtureWrite(f.t, root, directory+"/go.mod", "module github.com/Azure/ARO-HCP/"+directory+"\n\ngo 1.26.0\n\nrequire example.com/library v1.0.0\n")
	}
	for _, directory := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
		candidateFixtureWrite(f.t, root, directory+"modules.txt", "# example.com/library v1.0.0\n## explicit; go 1.26.0\nexample.com/library\n")
		candidateFixtureWrite(f.t, root, directory+"example.com/library/library.go", "package library\nconst Version = 1\n")
	}
	candidateFixtureGit(f.t, root, "add", ".")
	candidateFixtureGit(f.t, root, "commit", "-m", "Go base")
	f.base = candidateFixtureGit(f.t, root, "rev-parse", "HEAD")
	candidateFixtureWrite(f.t, root, "internal/go.mod", "module github.com/Azure/ARO-HCP/internal\n\ngo 1.26.0\n\nrequire example.com/library v1.1.0\n")
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte("go:example.com/library")))[:24]
	f.options.Group, f.options.Target = identity, "v1.1.0"
	f.group = map[string]any{"group": identity, "ecosystem": "go", "family": "example.com/library", "target": f.options.Target, "blockers": []any{}, "next_action": map[string]any{"action": "prepare_replacement"}, "sources": []any{map[string]any{"number": 42, "head_sha": strings.Repeat("a", 40), "group": identity, "ecosystem": "go", "family": "example.com/library", "target": f.options.Target, "intents": []any{map[string]any{"ecosystem": "go", "package": "example.com/library", "old": "v1.0.0", "target": f.options.Target, "manifest": "internal/go.mod", "direct": true, "key": "example.com/library"}}}}}
	content, err := json.Marshal(map[string]any{"repository": f.options.Repository, "base_sha": f.base, "groups": []any{f.group}})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.options.Inventory, content, 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.effect = f.generateGo
}

func (f *nativeCandidateFixture) generateGo(root string, command nativeCandidateCommand) error {
	if command.name == "go-all-tidy" || command.name == "go-all-tidy-fixpoint" {
		candidateFixtureWrite(f.t, root, "image-sync/oc-mirror/acrauth/go.mod", "module github.com/Azure/ARO-HCP/image-sync/oc-mirror/acrauth\n\ngo 1.26.0\n\nrequire example.com/library v1.1.0\n")
	}
	if command.name == "go-vendor" {
		for _, directory := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
			candidateFixtureWrite(f.t, root, directory+"modules.txt", "# example.com/library v1.1.0\n## explicit; go 1.26.0\nexample.com/library\n")
			candidateFixtureWrite(f.t, root, directory+"example.com/library/library.go", "package library\nconst Version = 2\n")
		}
	}
	return nil
}

func TestNativeCandidateGoGenerationAndIndependentConsumers(t *testing.T) {
	f := newCandidateFixture(t)
	f.prepareGo()
	f.effect = func(root string, command nativeCandidateCommand) error {
		if command.name == "go-test-unit" {
			if head := candidateFixtureGit(t, root, "rev-parse", "HEAD"); head == f.base {
				t.Fatal("tests preceded local commit")
			}
		}
		return f.generateGo(root, command)
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	names := map[string]nativeCandidateCommand{}
	for _, command := range f.commands {
		names[command.name] = command
	}
	for _, name := range []string{"go-all-tidy", "go-all-tidy-fixpoint", "go-vendor", "go-verify-vendor", "go-test-compile", "go-test-unit", "go-lint", "go-verify", "go-workspace", "go-build", "go-acrauth-build", "go-acrauth-test", "common-1", "common-2"} {
		if names[name].name == "" {
			t.Fatalf("missing %s", name)
		}
	}
	if !reflect.DeepEqual(names["go-acrauth-test"].environment, []string{"GOWORK=off"}) {
		t.Fatal("independent consumer inherited workspace")
	}
}

func TestNativeCandidateGoFixpointIncludesIgnoredVendor(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		t.Run(fmt.Sprint(ignored), func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareGo()
			candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/vendor/\n/image-sync/oc-mirror/acrauth/vendor/\n")
			f.effect = func(root string, command nativeCandidateCommand) error {
				if err := f.generateGo(root, command); err != nil {
					return err
				}
				if command.name == "go-all-tidy-fixpoint" {
					name := "internal/go.sum"
					if ignored {
						name = "vendor/example.com/library/new.go"
					}
					candidateFixtureWrite(t, root, name, "unstable output\n")
				}
				return nil
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), "second-run fixpoint") {
				t.Fatalf("error: %v", err)
			}
			for _, command := range f.commands {
				if command.name == "go-vendor" {
					t.Fatal("continued after nonconvergence")
				}
			}
			if head := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD"); head != f.base {
				t.Fatal("committed nonconverged output")
			}
		})
	}
}

func TestNativeCandidateStageRestoredCommittedBytesAndDeletedVendor(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f := newCandidateFixture(t)
			root := f.options.Root
			candidateFixtureWrite(t, root, "vendor/example/obsolete.go", "package example\n")
			candidateFixtureGit(t, root, "add", ".")
			candidateFixtureGit(t, root, "commit", "-m", "baseline obsolete vendor")
			base := candidateFixtureGit(t, root, "rev-parse", "HEAD")
			candidateFixtureWrite(t, root, candidateGeneratedFixture, "package generated\nconst Model = 2\n")
			candidateFixtureGit(t, root, "add", ".")
			candidateFixtureGit(t, root, "commit", "-m", "prior generated candidate")
			candidateFixtureWrite(t, root, candidateGeneratedFixture, "package generated\nconst Model = 0\n")
			candidateFixtureGit(t, root, "rm", "vendor/example/obsolete.go")
			if committed {
				candidateFixtureGit(t, root, "commit", "-m", "record vendor deletion")
			}
			index := filepath.Join(t.TempDir(), "index")
			if _, err := f.v.git(context.Background(), index, "read-tree", "HEAD"); err != nil {
				t.Fatal(err)
			}
			before := candidateFixtureGit(t, root, "write-tree")
			if err := f.v.stage(context.Background(), base, index); err != nil {
				t.Fatal(err)
			}
			tree, err := f.v.git(context.Background(), index, "write-tree")
			if err != nil {
				t.Fatal(err)
			}
			if after := candidateFixtureGit(t, root, "write-tree"); after != before {
				t.Fatal("isolated stage changed caller index")
			}
			if got := candidateFixtureGit(t, root, "show", tree+":"+candidateGeneratedFixture); got != "package generated\nconst Model = 0" {
				t.Fatal(got)
			}
			if got := candidateFixtureGit(t, root, "ls-tree", tree, "--", "vendor/example/obsolete.go"); got != "" {
				t.Fatal("restored obsolete vendor deletion")
			}
			if err := f.v.stage(context.Background(), base, ""); err != nil {
				t.Fatal(err)
			}
			if actual := candidateFixtureGit(t, root, "write-tree"); actual != tree {
				t.Fatal("real and isolated candidate indexes diverged")
			}
		})
	}
}

func (f *nativeCandidateFixture) saveInventory(groups ...map[string]any) {
	f.t.Helper()
	content, err := json.Marshal(map[string]any{
		"repository": f.options.Repository, "base_sha": f.base, "groups": groups,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.options.Inventory, content, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *nativeCandidateFixture) request() map[string]any {
	f.t.Helper()
	content, err := os.ReadFile(filepath.Join(f.options.Artifacts, nativeRequestFile))
	if err != nil {
		f.t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(content, &request); err != nil {
		f.t.Fatal(err)
	}
	return request
}

func (f *nativeCandidateFixture) noRequest() {
	f.t.Helper()
	if _, err := os.Lstat(filepath.Join(f.options.Artifacts, nativeRequestFile)); !os.IsNotExist(err) {
		f.t.Fatalf("failure left a request: %v", err)
	}
}

func (f *nativeCandidateFixture) prepareTypeSpec() {
	f.t.Helper()
	candidateFixtureGit(f.t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	f.packageName, f.family, f.old = "@typespec/compiler", "typespec", "1.16.0"
	f.options.Target = "1.17.0"
	f.npm(f.old)
	candidateFixtureGit(f.t, f.options.Root, "add", ".")
	candidateFixtureGit(f.t, f.options.Root, "commit", "-m", "TypeSpec baseline")
	f.base = candidateFixtureGit(f.t, f.options.Root, "rev-parse", "HEAD")
	f.npm(f.options.Target)
	f.observe()
	f.candidateOutput = "package generated\nconst Model = 2\n"
}

func TestNativeCandidateSelectsExactTargetAndRejectsInvalidIntentBeforeConsumers(t *testing.T) {
	t.Run("old-cleanup-does-not-shadow", func(t *testing.T) {
		f := newCandidateFixture(t)
		old := map[string]any{}
		content, err := json.Marshal(f.group)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(content, &old); err != nil {
			t.Fatal(err)
		}
		old["target"] = f.old
		source := old["sources"].([]any)[0].(map[string]any)
		source["number"], source["target"] = 41, f.old
		old["next_action"] = map[string]any{"action": "close_superseded", "replacement_pr": 100}
		f.saveInventory(old, f.group)
		if err := f.validate(); err != nil {
			t.Fatal(err)
		}
		sources := f.request()["source_prs"].([]any)
		if len(sources) != 1 || sources[0].(map[string]any)["number"] != float64(42) {
			t.Fatal(sources)
		}
		if f.v.results["target"] != f.options.Target {
			t.Fatal(f.v.results)
		}
	})
	t.Run("invalid-lock-path", func(t *testing.T) {
		f := newCandidateFixture(t)
		source := f.group["sources"].([]any)[0].(map[string]any)
		source["intents"].([]any)[0].(map[string]any)["lock_manifests"] = []any{"../outside"}
		f.saveInventory(f.group)
		if err := f.validate(); err == nil {
			t.Fatal("accepted invalid source intent")
		}
		f.noRequest()
		if len(f.commands) != 0 || candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
			t.Fatal("invalid source reached consumers or commit")
		}
	})
}

func TestNativeCandidateRequestDigestAndCompleteEvidence(t *testing.T) {
	f := newCandidateFixture(t)
	originalBody, err := os.ReadFile(f.options.BodyFile)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.v.validate(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(f.options.Artifacts, nativeRequestFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result["request_sha256"] != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		t.Fatal(result)
	}
	request := f.request()
	if len(request) != 5 || request["expected_base_sha"] != f.base {
		t.Fatal(request)
	}
	body := request["body"].(string)
	for _, expected := range []string{
		"**npm-api-models**: passed (exit 0), `make -C api models`",
		f.base, candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD"),
		"local consumer results, not source-PR CI or deployed-state evidence",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing evidence %q", expected)
		}
	}
	currentBody, err := os.ReadFile(f.options.BodyFile)
	if err != nil || string(currentBody) != string(originalBody) {
		t.Fatal("changed proposed publication body")
	}
	names := make([]string, 0, len(f.commands))
	for _, command := range f.commands {
		names = append(names, command.name)
		if command.name == "base-api-lint-openapi" {
			t.Fatal("baseline ran a nongenerator")
		}
	}
	if !reflect.DeepEqual(names[len(names)-2:], []string{"common-1", "common-2"}) {
		t.Fatal(names)
	}
	for _, target := range nativeAPITargets {
		found := false
		for _, name := range names {
			found = found || name == "npm-api-"+target
		}
		if !found {
			t.Fatalf("missing API target %s", target)
		}
	}
}

func TestNativeCandidateFailureEvidencePreservesUnrelatedArtifacts(t *testing.T) {
	f := newCandidateFixture(t)
	candidateFixtureWrite(t, f.options.Artifacts, nativeRequestFile, "stale request\n")
	candidateFixtureWrite(t, f.options.Artifacts, "keep.log", "previous evidence\n")
	f.effect = func(_ string, command nativeCandidateCommand) error {
		if command.name == "npm-api-swagger" {
			return fmt.Errorf("swagger exited 17; full log retained")
		}
		return nil
	}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "swagger exited 17") {
		t.Fatal(err)
	}
	f.noRequest()
	content, err := os.ReadFile(filepath.Join(f.options.Artifacts, "keep.log"))
	if err != nil || string(content) != "previous evidence\n" {
		t.Fatal("discarded unrelated artifacts")
	}
	content, err = os.ReadFile(filepath.Join(f.options.Artifacts, nativeCandidateResults))
	if err != nil || !strings.Contains(string(content), `"status": "failed"`) {
		t.Fatalf("missing failed machine evidence: %s: %v", content, err)
	}
	if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
		t.Fatal("committed failed generation")
	}
	for _, command := range f.commands {
		if strings.HasPrefix(command.name, "common-") {
			t.Fatal("continued after failed generation")
		}
	}
}

func TestNativeCandidateTypeSpecGeneratedConsumers(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareTypeSpec()
			f.effect = func(root string, command nativeCandidateCommand) error {
				if command.name == "npm-generated-internal-test" {
					if candidateFixtureGit(t, root, "rev-parse", "HEAD") == f.base {
						t.Fatal("generated consumer preceded candidate commit")
					}
					if !reflect.DeepEqual(command.argv, []string{"go", "test", "-timeout", "1200s", "./internal/azureapi/..."}) {
						t.Fatal(command.argv)
					}
					if fail {
						return fmt.Errorf("generated internal Go consumer failed")
					}
				}
				return nil
			}
			err := f.validate()
			if fail {
				if err == nil || !strings.Contains(err.Error(), "generated internal Go consumer failed") {
					t.Fatal(err)
				}
				f.noRequest()
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(f.request()["patch"].(string), candidateGeneratedFixture) {
					t.Fatal("discarded candidate-specific generation")
				}
				if !reflect.DeepEqual(f.v.results["excluded_baseline_identical_generation"], []string{}) {
					t.Fatal(f.v.results)
				}
			}
			content, err := os.ReadFile(filepath.Join(f.options.Root, candidateGeneratedFixture))
			if err != nil || string(content) != f.candidateOutput {
				t.Fatal("changed candidate-specific generated bytes")
			}
		})
	}
}

func (f *nativeCandidateFixture) prepareSDK() {
	f.t.Helper()
	f.prepareTypeSpec()
	candidateFixtureGit(f.t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	candidateFixtureWrite(f.t, f.options.Root, candidateSDKFixture+"/go.mod", "module github.com/Azure/ARO-HCP/"+candidateSDKFixture+"\n\ngo 1.26.0\n")
	for _, directory := range []string{candidateSDKFixture, "vendor/github.com/Azure/ARO-HCP/" + candidateSDKFixture} {
		candidateFixtureWrite(f.t, f.options.Root, directory+"/models.go", "package generated\nconst Model = 0\n")
		candidateFixtureWrite(f.t, f.options.Root, directory+"/LICENSE", "SDK license baseline\n")
	}
	candidateFixtureWrite(f.t, f.options.Root, "Makefile", "verify-yamlfmt:\n\ttrue\nverify-vendor:\n\tcmp "+candidateSDKFixture+"/models.go vendor/github.com/Azure/ARO-HCP/"+candidateSDKFixture+"/models.go\n\tcmp "+candidateSDKFixture+"/LICENSE vendor/github.com/Azure/ARO-HCP/"+candidateSDKFixture+"/LICENSE\n")
	candidateFixtureGit(f.t, f.options.Root, "add", ".")
	candidateFixtureGit(f.t, f.options.Root, "commit", "-m", "SDK and vendored consumer base")
	f.base = candidateFixtureGit(f.t, f.options.Root, "rev-parse", "HEAD")
	f.npm(f.options.Target)
	f.observe()
	f.candidateOutput = "package generated\nconst Model = 0\n"
}

func TestNativeCandidateSDKTestsAndRealVendorConsumerBlockPublication(t *testing.T) {
	for _, failure := range []string{"sdk-test", "stale-source", "stale-license"} {
		t.Run(failure, func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareSDK()
			tested, vendorChecked := false, false
			f.effect = func(root string, command nativeCandidateCommand) error {
				if command.name == "npm-api-testsdk" {
					name, text := "models.go", "package generated\nconst Model = 2\n"
					if failure == "stale-license" {
						name, text = "LICENSE", "SDK license changed\n"
					}
					candidateFixtureWrite(t, root, candidateSDKFixture+"/"+name, text)
				}
				if strings.HasPrefix(command.name, "npm-generated-sdk-test-") {
					tested = true
					if command.directory != candidateSDKFixture || !reflect.DeepEqual(command.environment, []string{"GOWORK=off"}) {
						t.Fatal(command)
					}
					if failure == "sdk-test" {
						return fmt.Errorf("generated SDK consumer failed")
					}
				}
				if command.name == "npm-generated-verify-vendor" {
					vendorChecked = true
					commandResult := nativeCandidateRun(context.Background(), root, command, t.TempDir())
					if commandResult.Status != model.ConsumerFailed {
						t.Fatalf("real vendor consumer did not fail: %+v", commandResult)
					}
					return fmt.Errorf("generated SDK vendor is stale")
				}
				return nil
			}
			err := f.validate()
			expected := "generated SDK vendor is stale"
			if failure == "sdk-test" {
				expected = "generated SDK consumer failed"
			}
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatal(err)
			}
			f.noRequest()
			if tested != (failure != "stale-license") || vendorChecked != (failure != "sdk-test") {
				t.Fatalf("SDK tested=%t vendor checked=%t", tested, vendorChecked)
			}
		})
	}
}

func TestNativeCandidateTypeSpecRecipesCannotBeDisabled(t *testing.T) {
	for _, phase := range []string{"preflight", "npm-api-testsdk"} {
		t.Run(phase, func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareTypeSpec()
			disable := func() {
				candidateFixtureWrite(t, f.options.Root, "api/Makefile", "models:\n\ttrue # disabled\n")
			}
			if phase == "preflight" {
				disable()
			}
			f.effect = func(_ string, command nativeCandidateCommand) error {
				if command.name == phase {
					disable()
				}
				return nil
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), "protected or unrelated replacement path") {
				t.Fatal(err)
			}
			f.noRequest()
			if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
				t.Fatal("committed disabled consumer recipes")
			}
			if phase == "preflight" && len(f.commands) != 0 {
				t.Fatal("executed altered recipes")
			}
		})
	}
}

func TestNativeCandidateRestoredCommittedGenerationIsRerunnable(t *testing.T) {
	f := newCandidateFixture(t)
	f.prepareTypeSpec()
	candidateFixtureWrite(t, f.options.Root, candidateGeneratedFixture, f.candidateOutput)
	candidateFixtureGit(t, f.options.Root, "add", ".")
	candidateFixtureGit(t, f.options.Root, "commit", "-m", "previous generated candidate")
	previous := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
	f.candidateOutput = "package generated\nconst Model = 0\n"
	candidateFixtureWrite(t, f.options.Root, candidateGeneratedFixture, f.candidateOutput)
	tree, changed, err := f.v.snapshot(context.Background(), f.base, f.group, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range changed {
		if name == candidateGeneratedFixture {
			t.Fatal("snapshot ignored restored worktree bytes")
		}
	}
	if candidateFixtureGit(t, f.options.Root, "show", ":"+candidateGeneratedFixture) == strings.TrimSpace(f.candidateOutput) {
		t.Fatal("snapshot changed real index")
	}
	for attempt := range 2 {
		if err := f.validate(); err != nil {
			t.Fatal(err)
		}
		if candidateFixtureGit(t, f.options.Root, "write-tree") != tree || f.v.results["candidate_tree"] != tree {
			t.Fatal("snapshot and final tree diverged")
		}
		current := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
		if attempt == 0 && current == previous {
			t.Fatal("did not commit restored bytes")
		}
		if attempt == 1 && current != previous {
			t.Fatal("rerun created an unnecessary commit")
		}
		if strings.Contains(f.request()["patch"].(string), candidateGeneratedFixture) {
			t.Fatal("packaged restored baseline bytes")
		}
		previous = current
	}
}

func TestNativeCandidateLateRequestRacesRemoveArtifact(t *testing.T) {
	for _, mutation := range []string{"worktree", "head", "body", "title", "inventory", "validated-body", "deleted-body", "ignored-vendor"} {
		t.Run(mutation, func(t *testing.T) {
			f := newCandidateFixture(t)
			candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/vendor/\n")
			f.v.prepare = func(ctx context.Context, options NativePrepareOptions) (map[string]string, error) {
				result, err := PrepareNativeRequest(ctx, options)
				if err != nil {
					return nil, err
				}
				switch mutation {
				case "worktree":
					candidateFixtureWrite(t, f.options.Root, "late-output.txt", "late write\n")
				case "ignored-vendor":
					candidateFixtureWrite(t, f.options.Root, "vendor/example/late.go", "package example\n")
				case "head":
					candidateFixtureGit(t, f.options.Root, "commit", "--allow-empty", "-m", "late clean commit")
				case "body":
					candidateFixtureWrite(t, filepath.Dir(f.options.BodyFile), filepath.Base(f.options.BodyFile), "late body\n")
				case "deleted-body":
					if err := os.Remove(f.options.BodyFile); err != nil {
						t.Fatal(err)
					}
				case "title":
					candidateFixtureWrite(t, filepath.Dir(f.options.TitleFile), filepath.Base(f.options.TitleFile), "late title\n")
				case "inventory":
					candidateFixtureWrite(t, filepath.Dir(f.options.Inventory), filepath.Base(f.options.Inventory), "{}\n")
				case "validated-body":
					if err := os.WriteFile(options.BodyFile, []byte("late evidence\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return result, nil
			}
			err := f.validate()
			if err == nil {
				t.Fatal("accepted late request race")
			}
			f.noRequest()
			if f.v.results["status"] != "failed" {
				t.Fatal(f.v.results)
			}
		})
	}
}

func TestNativeCandidateWrongAncestryRejectsBeforeConsumers(t *testing.T) {
	f := newCandidateFixture(t)
	candidateFixtureGit(t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	candidateFixtureGit(t, f.options.Root, "checkout", "--orphan", "unrelated")
	candidateFixtureGit(t, f.options.Root, "commit", "-m", "unrelated root")
	if err := f.validate(); err == nil {
		t.Fatal("accepted unrelated candidate HEAD")
	}
	f.noRequest()
	if len(f.commands) != 0 {
		t.Fatal("executed unrelated candidate")
	}
}

func TestNativeCandidateGoUnitFailureRetainsCommitWithoutRequest(t *testing.T) {
	f := newCandidateFixture(t)
	f.prepareGo()
	f.effect = func(root string, command nativeCandidateCommand) error {
		if err := f.generateGo(root, command); err != nil {
			return err
		}
		if command.name == "go-test-unit" {
			return fmt.Errorf("workspace unit tests failed")
		}
		return nil
	}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "workspace unit tests failed") {
		t.Fatal(err)
	}
	f.noRequest()
	if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") == f.base {
		t.Fatal("tests did not use the committed candidate")
	}
}

func TestNativeCandidateGoVendorAdditionsAndDeletionsSurviveReruns(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareGo()
			vendors := []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"}
			for _, prefix := range vendors {
				candidateFixtureWrite(t, f.options.Root, prefix+"example.com/library/obsolete.go", "package library\nconst Obsolete = true\n")
			}
			candidateFixtureGit(t, f.options.Root, "add", "--", "vendor/example.com/library/obsolete.go", "image-sync/oc-mirror/acrauth/vendor/example.com/library/obsolete.go")
			candidateFixtureGit(t, f.options.Root, "commit", "-m", "vendor deletion base")
			f.base = candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
			f.saveInventory(f.group)
			for _, prefix := range vendors {
				candidateFixtureGit(t, f.options.Root, "rm", "--", prefix+"example.com/library/obsolete.go")
			}
			if committed {
				candidateFixtureGit(t, f.options.Root, "commit", "-m", "record vendor removals")
			}
			candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/vendor/\n/image-sync/oc-mirror/acrauth/vendor/\n/scratch-cache/\n")
			for _, prefix := range vendors {
				candidateFixtureWrite(t, f.options.Root, prefix+"example.com/library/initial.go", "package library\nconst Added = 3\n")
			}
			f.effect = func(root string, command nativeCandidateCommand) error {
				if err := f.generateGo(root, command); err != nil {
					return err
				}
				candidateFixtureWrite(t, root, "scratch-cache/ignored.txt", command.name)
				if command.name == "go-vendor" {
					for _, prefix := range vendors {
						candidateFixtureWrite(t, root, prefix+"example.com/library/added.go", "package library\nconst Added = 3\n")
					}
				}
				if command.name == "go-verify-vendor" {
					for _, prefix := range vendors {
						for _, suffix := range []string{"initial.go", "added.go"} {
							name := prefix + "example.com/library/" + suffix
							if candidateFixtureGit(t, root, "show", "HEAD:"+name) != "package library\nconst Added = 3" {
								t.Fatalf("ignored vendor addition not committed: %s", name)
							}
						}
					}
				}
				return nil
			}
			if err := f.validate(); err != nil {
				t.Fatal(err)
			}
			patch := f.request()["patch"].(string)
			for _, prefix := range vendors {
				for _, suffix := range []string{"initial.go", "added.go", "obsolete.go"} {
					if !strings.Contains(patch, prefix+"example.com/library/"+suffix) {
						t.Fatalf("missing vendor addition/deletion %s%s", prefix, suffix)
					}
				}
				if _, err := os.Stat(filepath.Join(f.options.Root, prefix+"example.com/library/obsolete.go")); !os.IsNotExist(err) {
					t.Fatal("restored obsolete vendor file")
				}
			}
			if strings.Contains(patch, "scratch-cache") || candidateFixtureGit(t, f.options.Root, "ls-files", "--", "scratch-cache") != "" {
				t.Fatal("packaged ignored scratch cache")
			}
			tree := candidateFixtureGit(t, f.options.Root, "write-tree")
			head := candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
			if err := f.validate(); err != nil {
				t.Fatal(err)
			}
			if candidateFixtureGit(t, f.options.Root, "write-tree") != tree || candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != head || f.request()["patch"] != patch {
				t.Fatal("vendor rerun changed request or commit")
			}
		})
	}
}

func (f *nativeCandidateFixture) addNPMConsumer(valid bool) {
	f.t.Helper()
	candidateFixtureGit(f.t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	write := func(version string) {
		candidateFixtureWrite(f.t, f.options.Root, "other/package.json", fmt.Sprintf("{\"devDependencies\":{\"prettier\":%q}}\n", version))
		candidateFixtureWrite(f.t, f.options.Root, "other/package-lock.json", fmt.Sprintf("{\"lockfileVersion\":3,\"packages\":{\"\":{\"devDependencies\":{\"prettier\":%q}},\"node_modules/prettier\":{\"version\":%q}}}\n", version, version))
	}
	write(f.old)
	makefile := "build:\n\ttrue\n"
	if valid {
		makefile = "fmt:\n\ttrue\nlint:\n\ttrue\ntest:\n\ttrue\n"
	}
	candidateFixtureWrite(f.t, f.options.Root, "other/Makefile", makefile)
	candidateFixtureGit(f.t, f.options.Root, "add", ".")
	candidateFixtureGit(f.t, f.options.Root, "commit", "-m", "another group consumer")
	f.base = candidateFixtureGit(f.t, f.options.Root, "rev-parse", "HEAD")
	f.npm(f.options.Target)
	write(f.options.Target)
	f.observe()
	source := map[string]any{
		"number": 43, "head_sha": strings.Repeat("b", 40), "group": f.options.Group,
		"ecosystem": "npm", "family": f.family, "target": f.options.Target,
		"intents": []any{map[string]any{
			"ecosystem": "npm", "package": f.packageName, "old": f.old,
			"target": f.options.Target, "manifest": "other/package.json",
			"direct": true, "key": []any{"devDependencies", f.packageName},
			"lock_manifests": []any{"other/package-lock.json"},
		}},
	}
	f.group["sources"] = append(f.group["sources"].([]any), source)
	f.saveInventory(f.group)
}

func TestNativeCandidateEveryNPMSourceRequiresSupportedPassingConsumers(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			f := newCandidateFixture(t)
			f.addNPMConsumer(supported)
			f.effect = func(_ string, command nativeCandidateCommand) error {
				if command.name == "npm-other-test" {
					return fmt.Errorf("other source consumer failed")
				}
				return nil
			}
			expected := "unsupported npm consumer other: missing fixed Make consumers"
			if supported {
				expected = "other source consumer failed"
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatal(err)
			}
			f.noRequest()
			if !supported && len(f.commands) != 0 {
				t.Fatal("unsupported source reached consumers")
			}
			if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
				t.Fatal("committed incomplete group validation")
			}
		})
	}
}

func TestNativeCandidateNPMTransitiveIntentsOutsidePrimaryFamily(t *testing.T) {
	f := newCandidateFixture(t)
	candidateFixtureGit(t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	lock := func(direct, transitive string) {
		candidateFixtureWrite(t, f.options.Root, "api/package-lock.json", fmt.Sprintf("{\"lockfileVersion\":3,\"packages\":{\"\":{\"devDependencies\":{\"prettier\":%q}},\"node_modules/prettier\":{\"version\":%q,\"resolved\":\"https://registry.npmjs.org/fixture\"},\"node_modules/transitive\":{\"version\":%q}}}\n", direct, direct, transitive))
	}
	lock(f.old, "1.0.0")
	candidateFixtureGit(t, f.options.Root, "add", ".")
	candidateFixtureGit(t, f.options.Root, "commit", "-m", "transitive dependency baseline")
	f.base = candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD")
	f.npm(f.options.Target)
	lock(f.options.Target, "1.1.0")
	f.observe()
	source := f.group["sources"].([]any)[0].(map[string]any)
	source["intents"] = append(source["intents"].([]any), map[string]any{
		"ecosystem": "npm", "package": "transitive", "old": "1.0.0", "target": "1.1.0",
		"manifest": "api/package-lock.json", "key": "node_modules/transitive", "direct": false,
	})
	f.saveInventory(f.group)
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.request()["patch"].(string), `"1.1.0"`) {
		t.Fatal("request omitted validated transitive intent")
	}
}

// PrepareActions creates an Actions candidate with official tag-list evidence.
func (f *nativeCandidateFixture) prepareActions(agentic bool) (string, string) {
	f.t.Helper()
	candidateFixtureGit(f.t, f.options.Root, "restore", "api/package.json", "api/package-lock.json")
	oldPin, newPin := strings.Repeat("1", 40), strings.Repeat("2", 40)
	workflow := "name: CI\non: push\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@" + oldPin + "\n"
	manifest := ".github/workflows/ci.yml"
	if agentic {
		manifest = ".github/workflows/agent.md"
		candidateFixtureWrite(f.t, f.options.Root, ".github/workflows/agent.lock.yml", workflow)
		candidateFixtureWrite(f.t, f.options.Root, ".github/aw/actions-lock.json", candidateActionLock(oldPin))
	}
	candidateFixtureWrite(f.t, f.options.Root, manifest, workflow)
	candidateFixtureGit(f.t, f.options.Root, "add", ".")
	candidateFixtureGit(f.t, f.options.Root, "commit", "-m", "Actions consumer baseline")
	f.base = candidateFixtureGit(f.t, f.options.Root, "rev-parse", "HEAD")
	workflow = strings.ReplaceAll(workflow, oldPin, newPin)
	candidateFixtureWrite(f.t, f.options.Root, manifest, workflow)
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte("actions:actions/checkout")))[:24]
	f.options.Group, f.options.Target = identity, newPin
	f.group = map[string]any{
		"group": identity, "ecosystem": "actions", "family": "actions/checkout",
		"target": newPin, "blockers": []any{},
		"next_action": map[string]any{"action": "prepare_replacement"},
		"sources": []any{map[string]any{
			"number": 42, "head_sha": strings.Repeat("a", 40), "group": identity,
			"ecosystem": "actions", "family": "actions/checkout", "target": newPin,
			"intents": []any{map[string]any{
				"ecosystem": "actions", "package": "actions/checkout", "old": oldPin,
				"target": newPin, "manifest": manifest,
				"key": []any{6, "actions/checkout"}, "direct": true,
			}},
		}},
	}
	f.saveInventory(f.group)
	compiler, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	f.options.Compiler = compiler
	f.v.read = func(_ context.Context, endpoint string, pages, ci bool) (json.RawMessage, error) {
		if endpoint != "repos/actions/checkout/tags?per_page=100&page=1" || pages || ci {
			f.t.Fatalf("unsupported official read %q", endpoint)
		}
		return json.RawMessage(fmt.Sprintf("[{\"name\":\"v1\",\"commit\":{\"sha\":%q}}]", newPin)), nil
	}
	return workflow, newPin
}

func candidateActionLock(pin string) string {
	return fmt.Sprintf("{\"entries\":{\"actions/checkout@v1\":{\"repo\":\"actions/checkout\",\"version\":\"v1\",\"sha\":%q}}}\n", pin)
}

func TestNativeCandidateOrdinaryActionsYAMLFailureBlocksPublication(t *testing.T) {
	f := newCandidateFixture(t)
	f.prepareActions(false)
	f.effect = func(_ string, command nativeCandidateCommand) error {
		if command.argv[0] == "yamllint" {
			return fmt.Errorf("ordinary workflow YAML validation failed")
		}
		return nil
	}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "ordinary workflow YAML validation failed") {
		t.Fatal(err)
	}
	f.noRequest()
	if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
		t.Fatal("committed failed YAML validation")
	}
}

func TestNativeCandidateActionsPreflightDefersOnlyGeneratedCoverage(t *testing.T) {
	f := newCandidateFixture(t)
	_, newPin := f.prepareActions(true)
	candidateFixtureWrite(t, f.options.Root, ".github/aw/actions-lock.json", candidateActionLock(newPin))
	tree, changed, err := f.v.snapshot(context.Background(), f.base, f.group, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := NativeValidateTree(context.Background(), f.v.read, f.options.Root, f.base, tree, changed, f.group, true); err == nil {
		t.Fatal("strict guard accepted stale generated Actions coverage")
	}
	f.noRequest()
}

func TestNativeCandidateActionsRegeneratesBeforeStrictGuard(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(fmt.Sprint(unsafe), func(t *testing.T) {
			f := newCandidateFixture(t)
			workflow, newPin := f.prepareActions(true)
			f.effect = func(root string, command nativeCandidateCommand) error {
				if command.name == "actions-agent" {
					output := workflow
					if unsafe {
						output = strings.ReplaceAll(output, "on: push", "on: pull_request_target")
					}
					candidateFixtureWrite(t, root, ".github/workflows/agent.lock.yml", output)
					candidateFixtureWrite(t, root, ".github/aw/actions-lock.json", candidateActionLock(newPin))
				}
				return nil
			}
			err := f.validate()
			if unsafe {
				if err == nil || !strings.Contains(err.Error(), "non-pin Actions source change") {
					t.Fatal(err)
				}
				f.noRequest()
				if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
					t.Fatal("committed unsafe workflow behavior")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				patch := f.request()["patch"].(string)
				for _, name := range []string{".github/workflows/agent.md", ".github/workflows/agent.lock.yml", ".github/aw/actions-lock.json"} {
					if !strings.Contains(patch, name) {
						t.Fatalf("missing generated Actions consumer: %s", name)
					}
				}
			}
		})
	}
}

func TestNativeCandidateFailedAttemptEvidenceIsImmutable(t *testing.T) {
	f := newCandidateFixture(t)
	f.effect = func(_ string, command nativeCandidateCommand) error {
		if command.name == "npm-api-swagger" {
			return fmt.Errorf("first attempt failed")
		}
		return nil
	}
	if err := f.validate(); err == nil {
		t.Fatal("expected failed first attempt")
	}
	firstPath := filepath.Join(f.v.attempt, nativeCandidateResults)
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newNativeCandidate(f.options.Root, f.options.Artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	second.execute = f.consume
	f.effect = nil
	if _, err := second.validate(context.Background(), f.options); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(firstPath)
	if err != nil || string(after) != string(first) || second.attempt == f.v.attempt {
		t.Fatal("later attempt overwrote failed machine evidence")
	}
}

func TestNativeCandidateFixpointDetectsTrackedIdentityAndIndependentVendorChanges(t *testing.T) {
	for _, change := range []string{"tracked-delete", "symlink-target", "executable", "independent-ignored-vendor"} {
		t.Run(change, func(t *testing.T) {
			f := newCandidateFixture(t)
			f.prepareGo()
			candidateFixtureWrite(t, f.options.Root, ".git/info/exclude", "/image-sync/oc-mirror/acrauth/vendor/\n")
			f.effect = func(root string, command nativeCandidateCommand) error {
				if err := f.generateGo(root, command); err != nil {
					return err
				}
				if command.name != "go-all-tidy-fixpoint" {
					return nil
				}
				switch change {
				case "tracked-delete":
					return os.Remove(filepath.Join(root, "internal/go.mod"))
				case "symlink-target":
					name := filepath.Join(root, "vendor/example/notice-link")
					if err := os.Remove(name); err != nil {
						return err
					}
					return os.Symlink("different-target", name)
				case "executable":
					return os.Chmod(filepath.Join(root, "internal/go.mod"), 0o700)
				default:
					candidateFixtureWrite(t, root, "image-sync/oc-mirror/acrauth/vendor/example.com/library/new.go", "package library\n")
					return nil
				}
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), "second-run fixpoint") {
				t.Fatal(err)
			}
			f.noRequest()
			if candidateFixtureGit(t, f.options.Root, "rev-parse", "HEAD") != f.base {
				t.Fatal("committed unstable file identity")
			}
		})
	}
}

func TestNativeCandidateMissingConsumerRecordsFailureAndLog(t *testing.T) {
	f := newCandidateFixture(t)
	f.v.execute = nativeCandidateRun
	_, err := f.v.run(context.Background(), nativeCandidateCommand{
		name: "missing", argv: []string{filepath.Join(t.TempDir(), "absent")}, directory: ".",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Fatal(err)
	}
	step := f.v.steps[0]
	if step["status"] != "failed" || step["timeout_seconds"] != 1800 || step["log"] == "" {
		t.Fatal(step)
	}
}

func TestNativeCandidateOverflowFailsEntireRequestLifecycle(t *testing.T) {
	f := newCandidateFixture(t)
	original := strings.Repeat("x", 20000)
	candidateFixtureWrite(t, filepath.Dir(f.options.BodyFile), filepath.Base(f.options.BodyFile), original)
	candidateFixtureWrite(t, f.options.Artifacts, nativeRequestFile, "stale success")
	if err := f.validate(); err == nil {
		t.Fatal("published an evidence-inclusive body above the text bound")
	}
	f.noRequest()
	body, err := os.ReadFile(f.options.BodyFile)
	if err != nil || string(body) != original {
		t.Fatalf("modified original publication body: %v", err)
	}
	if f.v.results["status"] != "failed" {
		t.Fatalf("overflow did not fail validation: %#v", f.v.results)
	}
}

func TestNativeCandidateTimeoutPreservesMachineClassification(t *testing.T) {
	f := newCandidateFixture(t)
	f.v.execute = func(context.Context, string, nativeCandidateCommand, string) model.ConsumerResult {
		return model.ConsumerResult{Status: model.ConsumerFailed, ExitCode: -1, Reason: "consumer timed out", LogPath: "timeout.log"}
	}
	if _, err := f.v.run(context.Background(), nativeCandidateCommand{name: "timeout", argv: []string{"make", "verify"}, directory: "."}, ""); err == nil {
		t.Fatal("accepted timed-out consumer")
	}
	step := f.v.steps[len(f.v.steps)-1]
	if step["status"] != "timeout" || step["returncode"] != -1 || step["log"] != "timeout.log" {
		t.Fatalf("lost timeout classification: %#v", step)
	}
}
