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
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	repairManifest  = "api/package.json"
	repairMakefile  = "api/Makefile"
	repairGenerated = "internal/azureapi/v20261001preview/generated/models.go"
	repairBranch    = "refs/heads/bot/typespec"
	repairACRModule = "image-sync/oc-mirror/acrauth"
	repairVerifier  = "hack/verify-vendor.sh"
)

const repairGHStub = `#!/bin/sh
set -eu

if [ "$#" -eq 2 ] && [ "$1" = auth ] && [ "$2" = setup-git ]; then
    exit 0
fi
if [ "$#" -ge 2 ] && [ "$1" = api ] && [ "$2" = repos/Azure/ARO-HCP/pulls/42 ]; then
    sha=$(git --git-dir="$TEST_REMOTE" rev-parse refs/heads/bot/typespec)
    if [ "$#" -eq 2 ]; then
        printf '{"state":"open","user":{"login":"aro-hcp-robot[bot]"},"head":{"repo":{"full_name":"Azure/ARO-HCP"},"ref":"bot/typespec","sha":"%s"},"base":{"ref":"main"},"title":"fix(deps): typespec to 1.16.0","labels":[{"name":"agentic-dependabot"}]}\n' "$sha"
        exit 0
    fi
    if [ "$#" -eq 4 ] && [ "$3" = --jq ] && [ "$4" = '.head.sha' ]; then
        printf '%s\n' "$sha"
        exit 0
    fi
fi
printf 'Unexpected gh invocation: %s\n' "$*" | tee -a "$TEST_UNEXPECTED_GH" >&2
exit 1
`

type ownedPRRepairFixture struct {
	root, seed, remote string
	base, oldHead      string
	step, goBin        string
	env                []string
}

// TestOwnedPRRepairRebuild exercises the compiled repair step against conflicting bot and main commits.
func TestOwnedPRRepairRebuild(t *testing.T) {
	for _, tc := range []struct {
		name      string
		staleBase bool
	}{
		{name: "rebuild on main without a new PR"},
		{name: "reject stale main without moving the branch", staleBase: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOwnedPRRepairFixture(t)
			const mergedManifest = `{"typespec":"1.16.0","other":"new"}` + "\n"
			const fixedMakefile = "models:\n\t@trap cleanup EXIT; echo fixed\n"
			const fixedModels = "package generated\n\nconst Typespec = \"1.16.0\"\n"
			nativeCLIWrite(t, filepath.Join(f.seed, repairManifest), mergedManifest, 0600)
			nativeCLIWrite(t, filepath.Join(f.seed, repairMakefile), fixedMakefile, 0600)
			nativeCLIWrite(t, filepath.Join(f.seed, repairGenerated), fixedModels, 0600)
			patch := f.mustRun(t, f.seed, "git", "diff", "--", repairManifest, repairMakefile, repairGenerated)
			if tc.staleBase {
				f.runRepair(t, f.oldHead, patch, "main moved from the patch base")
				if got := strings.TrimSpace(f.remoteGit(t, "rev-parse", repairBranch)); got != f.oldHead {
					t.Fatalf("rejected repair moved branch to %s, want %s", got, f.oldHead)
				}
				return
			}

			f.runRepair(t, f.base, patch, "")
			newHead := strings.TrimSpace(f.remoteGit(t, "rev-parse", repairBranch))
			if newHead == f.oldHead {
				t.Fatalf("repair left branch at its old head %s", newHead)
			}
			if got := strings.TrimSpace(f.remoteGit(t, "rev-parse", newHead+"^")); got != f.base {
				t.Errorf("repaired parent = %s, want main %s", got, f.base)
			}
			for path, want := range map[string]string{repairManifest: mergedManifest, repairMakefile: fixedMakefile, repairGenerated: fixedModels} {
				if got := f.remoteGit(t, "show", newHead+":"+path); got != want {
					t.Errorf("repaired %s = %q, want %q", path, got, want)
				}
			}
			if got := f.remoteGit(t, "for-each-ref", "--format=%(refname)", "refs/heads"); got != repairBranch+"\nrefs/heads/main\n" {
				t.Errorf("repair created an unexpected branch: %s", got)
			}
		})
	}
}

// TestOwnedPRRepairVendor requires the actual verifier to reject stale vendor and accept regenerated vendor.
func TestOwnedPRRepairVendor(t *testing.T) {
	f := newOwnedPRRepairFixture(t)
	module := filepath.Join(f.seed, "go.mod")
	original, err := os.ReadFile(module)
	if err != nil {
		t.Fatalf("read fixture go.mod: %v", err)
	}
	updated := strings.ReplaceAll(string(original), "v1.0.0", "v1.1.0")
	nativeCLIWrite(t, module, updated, 0600)
	patch := f.mustRun(t, f.seed, "git", "diff", "--", "go.mod")
	f.runRepair(t, f.base, patch, "Vendored dependencies are out of date")
	if got := strings.TrimSpace(f.remoteGit(t, "rev-parse", repairBranch)); got != f.oldHead {
		t.Fatalf("rejected vendor repair moved branch to %s, want %s", got, f.oldHead)
	}

	f.mustRun(t, f.seed, f.goBin, "work", "vendor")
	patch = f.mustRun(t, f.seed, "git", "diff", "--", "go.mod", "vendor")
	f.runRepair(t, f.base, patch, "")
	if got := f.remoteGit(t, "show", repairBranch+":go.mod"); got != updated {
		t.Errorf("published go.mod = %q, want %q", got, updated)
	}
	if got := f.remoteGit(t, "show", repairBranch+":vendor/modules.txt"); !strings.Contains(got, "# example.com/dep v1.1.0") {
		t.Errorf("published vendor/modules.txt lacks regenerated dependency: %s", got)
	}
}

// newOwnedPRRepairFixture creates a local remote with a stale bot branch and a newer conflicting main.
func newOwnedPRRepairFixture(t *testing.T) ownedPRRepairFixture {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate Go for repair fixture: %v", err)
	}
	goDir := filepath.Dir(goBin)
	f := ownedPRRepairFixture{
		root:   root,
		remote: filepath.Join(root, "remote.git"),
		seed:   filepath.Join(root, "seed"),
		step:   compiledOwnedPRRepairStep(t),
		goBin:  goBin,
		env: []string{
			"PATH=" + strings.Join([]string{bin, goDir, os.Getenv("PATH")}, string(os.PathListSeparator)),
			"HOME=" + root,
			"LC_ALL=C",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
			"GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0",
			"GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOVCS=*:off",
			// Go can extract a bundled FIPS module even with the proxy disabled.
			"GOFLAGS=-modcacherw",
		},
	}
	nativeCLIWrite(t, filepath.Join(bin, "gh"), repairGHStub, 0700)
	verifyVendor, err := os.ReadFile(filepath.Join("..", "..", repairVerifier))
	if err != nil {
		t.Fatalf("read vendor verifier: %v", err)
	}
	for file, content := range map[string]string{
		repairManifest:              `{"typespec":"1.15.0"}` + "\n",
		repairMakefile:              "models:\n\t@echo old\n",
		repairGenerated:             "package generated\n",
		"go.work":                   "go 1.26.0\nuse .\n",
		"go.mod":                    "module example.com/app\n\ngo 1.26.0\nrequire example.com/dep v1.0.0\nreplace example.com/dep => ./dep\n",
		"app.go":                    "package app\nimport _ \"example.com/dep\"\n",
		"dep/go.mod":                "module example.com/dep\n\ngo 1.26.0\n",
		"dep/dep.go":                "package dep\n",
		"Makefile":                  "verify-vendor:\n\tbash " + repairVerifier + "\n",
		repairVerifier:              string(verifyVendor),
		repairACRModule + "/go.mod": "module example.com/acrauth\n\ngo 1.26.0\nrequire example.com/dep v1.0.0\nreplace example.com/dep => ../../../dep\n",
		repairACRModule + "/app.go": "package acrauth\nimport _ \"example.com/dep\"\n",
	} {
		nativeCLIWrite(t, filepath.Join(f.seed, file), content, 0600)
	}
	f.mustRun(t, root, "git", "init", "--bare", f.remote)
	f.mustRun(t, f.seed, "git", "init", "-b", "main")
	f.mustRun(t, f.seed, "git", "config", "user.name", "Test")
	f.mustRun(t, f.seed, "git", "config", "user.email", "test@example.com")
	f.mustRun(t, f.seed, f.goBin, "work", "vendor")
	f.mustRun(t, filepath.Join(f.seed, repairACRModule), "env", "GOWORK=off", f.goBin, "mod", "vendor")
	f.mustRun(t, f.seed, "git", "add", ".")
	f.mustRun(t, f.seed, "git", "commit", "-m", "original")
	f.mustRun(t, f.seed, "git", "remote", "add", "origin", f.remote)
	f.mustRun(t, f.seed, "git", "switch", "-c", "bot/typespec")
	nativeCLIWrite(t, filepath.Join(f.seed, repairManifest), `{"typespec":"1.16.0"}`+"\n", 0600)
	f.mustRun(t, f.seed, "git", "commit", "-am", "bump typespec")
	f.oldHead = strings.TrimSpace(f.mustRun(t, f.seed, "git", "rev-parse", "HEAD"))
	f.mustRun(t, f.seed, "git", "switch", "main")
	nativeCLIWrite(t, filepath.Join(f.seed, repairManifest), `{"typespec":"1.15.0","other":"new"}`+"\n", 0600)
	f.mustRun(t, f.seed, "git", "commit", "-am", "main change conflicting with the bump")
	f.base = strings.TrimSpace(f.mustRun(t, f.seed, "git", "rev-parse", "HEAD"))
	f.mustRun(t, f.seed, "git", "push", "origin", "main", "bot/typespec")
	return f
}

// compiledOwnedPRRepairStep extracts the literal run block of the named step in the generated job.
func compiledOwnedPRRepairStep(t *testing.T) string {
	t.Helper()
	const workflow = "../../.github/workflows/dependabot-remediation.lock.yml"
	data, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read compiled repair workflow: %v", err)
	}
	const step = "\n      - name: Verify and apply repair\n        run: |\n"
	_, after, found := strings.Cut(string(data), step)
	if !found {
		t.Fatalf("%s: missing literal run block for Verify and apply repair", workflow)
	}
	var script strings.Builder
	for line := range strings.SplitSeq(after, "\n") {
		if line != "" {
			var ok bool
			line, ok = strings.CutPrefix(line, "          ")
			if !ok {
				break
			}
		}
		script.WriteString(line)
		script.WriteByte('\n')
	}
	if strings.TrimSpace(script.String()) == "" {
		t.Fatalf("%s: empty Verify and apply repair run block", workflow)
	}
	return script.String()
}

// mustRun runs a fixture command offline and fails the test if setup or inspection fails.
func (f ownedPRRepairFixture) mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Env = f.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, output)
	}
	return string(output)
}

// remoteGit inspects the bare fixture repository rather than the runner checkout.
func (f ownedPRRepairFixture) remoteGit(t *testing.T, args ...string) string {
	t.Helper()
	return f.mustRun(t, f.root, "git", append([]string{"--git-dir", f.remote}, args...)...)
}

// runRepair executes the workflow script in a clean local checkout and checks its outcome and API boundary.
func (f ownedPRRepairFixture) runRepair(t *testing.T, base, patch, wantFailure string) {
	t.Helper()
	runDir := t.TempDir()
	runner := filepath.Join(runDir, "runner")
	f.mustRun(t, runDir, "git", "clone", "--branch", "main", f.remote, runner)
	envelope := map[string][]map[string]string{"items": {{
		"type":                "repair_owned_pr",
		"pull_request_number": "42",
		"expected_head_sha":   f.oldHead,
		"expected_base_sha":   base,
		"patch":               patch,
	}}}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal repair safe output: %v", err)
	}
	outputPath := filepath.Join(runDir, "agent-output.json")
	nativeCLIWrite(t, outputPath, string(payload), 0600)
	unexpectedGH := filepath.Join(runDir, "unexpected-gh")
	cmd := exec.CommandContext(t.Context(), "bash", "-c", f.step)
	cmd.Dir = runner
	cmd.Env = append(f.env, "GH_AW_AGENT_OUTPUT="+outputPath, "RUNNER_TEMP="+runDir,
		"GH_TOKEN=test-token", "REPO=Azure/ARO-HCP", "TEST_REMOTE="+f.remote, "TEST_UNEXPECTED_GH="+unexpectedGH)
	output, err := cmd.CombinedOutput()
	if unexpected, readErr := os.ReadFile(unexpectedGH); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("repair exceeded the GitHub stub boundary (read error: %v): %s\n%s", readErr, unexpected, output)
	}
	if wantFailure != "" {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || !strings.Contains(string(output), wantFailure) {
			t.Fatalf("repair: want failure containing %q, got %v\n%s", wantFailure, err, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, output)
	}
}
