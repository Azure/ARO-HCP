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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEPCTL_NATIVE_CLI_TEST_PROCESS") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func nativeCLIWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func nativeCLIGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func nativeCLIFixture(t *testing.T, fail bool) ([]string, string) {
	t.Helper()
	directory := t.TempDir()
	root := filepath.Join(directory, "candidate")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	writeVersion := func(version string) {
		nativeCLIWrite(t, filepath.Join(root, "ui/package.json"), `{"name":"fixture","devDependencies":{"prettier":"`+version+`"}}`, 0600)
		nativeCLIWrite(t, filepath.Join(root, "ui/package-lock.json"), `{"name":"fixture","lockfileVersion":3,"packages":{"":{"name":"fixture","devDependencies":{"prettier":"`+version+`"}},"node_modules/prettier":{"version":"`+version+`"}}}`, 0600)
	}
	writeVersion("3.9.9")
	lint := "\t@printf 'lint consumer output\\n'\n"
	if fail {
		lint += "\t@exit 23\n"
	}
	nativeCLIWrite(t, filepath.Join(root, "ui/Makefile"), "fmt:\n\t@printf 'format consumer output\\n'\nlint:\n"+lint+"test:\n\t@printf 'test consumer output\\n'\n", 0600)
	nativeCLIWrite(t, filepath.Join(root, "Makefile"), "verify-yamlfmt:\n\t@printf 'common consumer output\\n'\n", 0600)
	nativeCLIWrite(t, filepath.Join(root, "config/Makefile"), "detect-change:\n\t@printf 'config consumer output\\n'\n", 0600)
	nativeCLIGit(t, root, "init", "-b", "main")
	nativeCLIGit(t, root, "config", "user.name", "Fixture")
	nativeCLIGit(t, root, "config", "user.email", "fixture@example.test")
	nativeCLIGit(t, root, "add", ".")
	nativeCLIGit(t, root, "commit", "-m", "base")
	base := nativeCLIGit(t, root, "rev-parse", "HEAD")
	writeVersion("3.9.10")
	familyHash := sha256.Sum256([]byte("npm:prettier"))
	group := hex.EncodeToString(familyHash[:12])
	inventory := `{"repository":"Azure/ARO-HCP","base_sha":"` + base + `","groups":[{"group":"` + group + `","target":"3.9.10","ecosystem":"npm","family":"prettier","blockers":[],"next_action":{"action":"prepare_replacement"},"sources":[{"number":1,"head_sha":"` + strings.Repeat("b", 40) + `","group":"` + group + `","target":"3.9.10","ecosystem":"npm","family":"prettier","intents":[{"ecosystem":"npm","package":"prettier","manifest":"ui/package.json","key":["devDependencies","prettier"],"target":"3.9.10","direct":true,"lock_manifests":["ui/package-lock.json"]}]}]}]}`
	inventoryPath := filepath.Join(directory, "inventory.json")
	nativeCLIWrite(t, inventoryPath, inventory, 0600)
	title, body := filepath.Join(directory, "title.txt"), filepath.Join(directory, "body.txt")
	nativeCLIWrite(t, title, "chore(deps): update prettier", 0600)
	nativeCLIWrite(t, body, "Validate the complete fixture consumer.", 0600)
	bin := filepath.Join(directory, "bin")
	nativeCLIWrite(t, filepath.Join(bin, "npm"), "#!/bin/sh\nprintf 'npm consumer output\\n'\n", 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	artifacts := filepath.Join(directory, "artifacts")
	return []string{"dependabot", "native", "validate-candidate", "--repository", "Azure/ARO-HCP", "--repo", root, "--inventory", inventoryPath, "--group", group, "--target", "3.9.10", "--title-file", title, "--body-file", body, "--artifact-dir", artifacts}, artifacts
}

func nativeCLIProcess(t *testing.T, args []string) ([]byte, []byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), "DEPCTL_NATIVE_CLI_TEST_PROCESS=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestNativeCandidateCLIStdoutDigest(t *testing.T) {
	args, artifacts := nativeCLIFixture(t, false)
	stdout, stderr, err := nativeCLIProcess(t, args)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("CLI failed: %v, stderr=%s, stdout=%s", err, stderr, stdout)
	}
	var receipt map[string]string
	if err := json.Unmarshal(stdout, &receipt); err != nil || len(receipt) != 1 {
		t.Fatalf("stdout is not one digest receipt: %s (%v)", stdout, err)
	}
	payload, err := os.ReadFile(filepath.Join(artifacts, "native-replacement-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if receipt["request_sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatal("CLI receipt does not identify the prepared artifact")
	}
}

func TestNativeCandidateCLIStderrFailure(t *testing.T) {
	args, artifacts := nativeCLIFixture(t, true)
	t.Setenv("MAKEFLAGS", "-n")
	stdout, stderr, err := nativeCLIProcess(t, args)
	if err == nil || len(stdout) != 0 || len(stderr) == 0 {
		t.Fatalf("consumer failure did not stay on stderr: %v, stdout=%s, stderr=%s", err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(artifacts, "native-replacement-request.json")); !os.IsNotExist(err) {
		t.Fatalf("failed CLI left a publication artifact: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(artifacts, "native-validation-results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var results struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &results); err != nil || results.Status != "failed" {
		t.Fatalf("missing failed consumer evidence: %s (%v)", payload, err)
	}
}
