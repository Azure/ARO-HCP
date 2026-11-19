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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

const nativeCandidateResults = "native-validation-results.json"

var nativeAPITargets = []string{"fmt", "swagger", "lint-openapi", "models", "testsdk", "validate-examples"}
var nativeAPIGenerators = []string{"fmt", "swagger", "models", "testsdk"}
var nativeMakeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_. /-]+):([^=]|$)`)

// NativeCandidateOptions identifies an offline candidate and its publication inputs.
type NativeCandidateOptions struct {
	Repository, Root, Inventory, Group, Target, TitleFile, BodyFile, Artifacts, Compiler string
}

type nativeCandidateCommand struct {
	name        string
	argv        []string
	directory   string
	environment []string
}

type nativeCandidate struct {
	root, artifacts, attempt string
	repo                     *repository.Repo
	read                     NativeReadFunc
	results                  map[string]any
	steps                    []map[string]any
	// Execution is replaceable only inside package tests; production always uses the registry runner.
	execute func(context.Context, string, nativeCandidateCommand, string) model.ConsumerResult
	// Request construction is replaceable only by package boundary tests.
	prepare func(context.Context, NativePrepareOptions) (map[string]string, error)
}

// NativeCandidateRun executes a compiled command through the shared consumer registry.
func nativeCandidateRun(ctx context.Context, root string, command nativeCandidateCommand, logs string) model.ConsumerResult {
	spec := consumers.Spec{ID: command.name, Program: command.argv[0], Arguments: command.argv[1:], Directory: command.directory, Environment: append([]string{"CI=true"}, command.environment...), Timeout: 1800 * time.Second}
	registry, err := consumers.NewRegistry(spec)
	if err != nil {
		return model.ConsumerResult{Status: model.ConsumerError, Reason: err.Error()}
	}
	runner := consumers.NewRunner(registry)
	runner.FullLogs = true
	return runner.Run(ctx, root, model.ConsumerRef{ID: command.name}, logs)
}

// NewNativeCandidate isolates each attempt without removing unrelated artifacts.
func newNativeCandidate(root, artifacts string, read NativeReadFunc) (*nativeCandidate, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	artifacts, err = filepath.Abs(artifacts)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(artifacts, 0o755); err != nil {
		return nil, err
	}
	artifacts, err = filepath.EvalSymlinks(artifacts)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, artifacts)
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return nil, errors.New("ARTIFACT_DIR must be outside the candidate checkout")
	}
	if err = os.Remove(filepath.Join(artifacts, nativeRequestFile)); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	attempt, err := os.MkdirTemp(artifacts, "native-validation-")
	if err != nil {
		return nil, err
	}
	v := &nativeCandidate{
		root:      root,
		artifacts: artifacts,
		attempt:   attempt,
		repo:      &repository.Repo{Path: root},
		read:      read,
		execute:   nativeCandidateRun,
		prepare:   PrepareNativeRequest,
		steps:     []map[string]any{},
	}
	v.results = map[string]any{"status": "running", "attempt_directory": attempt}
	return v, v.save()
}

// Save retains both immutable-attempt and latest machine evidence.
func (v *nativeCandidate) save() error {
	v.results["steps"] = v.steps
	content, err := json.MarshalIndent(v.results, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	for _, directory := range []string{v.attempt, v.artifacts} {
		if err := os.WriteFile(filepath.Join(directory, nativeCandidateResults), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Git uses only the shared sanitized local Git boundary.
func (v *nativeCandidate) git(ctx context.Context, index string, args ...string) (string, error) {
	content, err := v.repo.NativeGit(ctx, nil, index, args...)
	return strings.TrimRight(string(content), "\r\n"), err
}

// Run records phase-specific commands without sending consumer output to stdout.
func (v *nativeCandidate) run(ctx context.Context, command nativeCandidateCommand, root string) (string, error) {
	if root == "" {
		root = v.root
	}
	environment := map[string]string{}
	for _, item := range command.environment {
		key, value, _ := strings.Cut(item, "=")
		environment[key] = value
	}
	step := map[string]any{"name": command.name, "argv": command.argv, "cwd": filepath.Join(root, command.directory), "environment": environment, "timeout_seconds": 1800, "status": "running"}
	v.steps = append(v.steps, step)
	if err := v.save(); err != nil {
		return "", err
	}
	result := v.execute(ctx, root, command, v.attempt)
	step["log"], step["returncode"], step["duration_seconds"] = result.LogPath, result.ExitCode, float64(result.DurationMS)/1000
	step["status"] = "passed"
	if result.Status != model.ConsumerPassed || result.ExitCode != 0 {
		step["status"] = "failed"
		if strings.Contains(result.Reason, "timed out") {
			step["status"] = "timeout"
		}
		step["error"] = result.Reason
	}
	if err := v.save(); err != nil {
		return "", err
	}
	if step["status"] == "timeout" {
		return "", fmt.Errorf("%s exceeded 1800s; full log: %s", command.name, result.LogPath)
	}
	if result.Status == model.ConsumerError || result.Status == model.ConsumerNotRun {
		return "", fmt.Errorf("%s could not start: %s; full log: %s", command.name, result.Reason, result.LogPath)
	}
	if step["status"] != "passed" {
		return "", fmt.Errorf("%s exited %d: %s; full log: %s", command.name, result.ExitCode, result.Reason, result.LogPath)
	}
	return result.LogPath, nil
}

// NativeCandidateNames splits Git's NUL-delimited names without trimming filenames.
func nativeCandidateNames(value string) []string {
	var names []string
	for _, name := range strings.Split(value, "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Untracked includes ignored generated vendor additions but excludes ignored caches.
func (v *nativeCandidate) untracked(ctx context.Context) ([]string, error) {
	ordinary, err := v.git(ctx, "", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	vendor, err := v.git(ctx, "", "ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--", "vendor/", "image-sync/oc-mirror/acrauth/vendor/")
	return append(nativeCandidateNames(ordinary), nativeCandidateNames(vendor)...), err
}

// Paths validates every changed path before staging or executing consumers.
func (v *nativeCandidate) paths(ctx context.Context, base string) ([]string, error) {
	changed, err := v.git(ctx, "", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	untracked, err := v.untracked(ctx)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range append(nativeCandidateNames(changed), untracked...) {
		if !safeNativePath(name) {
			return nil, fmt.Errorf("unsafe candidate path: %s", name)
		}
		set[name] = true
	}
	return nativeCandidateSorted(set), nil
}

// NativeCandidateSorted returns deterministic unique keys.
func nativeCandidateSorted(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Stage includes HEAD-relative restorations and already-staged vendor deletions.
func (v *nativeCandidate) stage(ctx context.Context, base, index string) error {
	set := map[string]bool{}
	for _, revision := range []string{base, "HEAD"} {
		names, err := v.paths(ctx, revision)
		if err != nil {
			return err
		}
		for _, name := range names {
			set[name] = true
		}
	}
	tracked, err := v.git(ctx, index, "ls-files", "-z")
	if err != nil {
		return err
	}
	trackedSet := map[string]bool{}
	for _, name := range nativeCandidateNames(tracked) {
		trackedSet[name] = true
	}
	var ordinary, vendor []string
	for _, name := range nativeCandidateSorted(set) {
		_, err := os.Lstat(filepath.Join(v.root, name))
		if !trackedSet[name] && os.IsNotExist(err) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if strings.HasPrefix(name, "vendor/") || strings.HasPrefix(name, "image-sync/oc-mirror/acrauth/vendor/") {
			vendor = append(vendor, name)
		} else {
			ordinary = append(ordinary, name)
		}
	}
	for _, scope := range []struct{ names, args []string }{{ordinary, []string{"add", "-A", "--"}}, {vendor, []string{"add", "-A", "--force", "--"}}} {
		if len(scope.names) > 0 {
			if _, err := v.git(ctx, index, append(scope.args, scope.names...)...); err != nil {
				return err
			}
		}
	}
	return nil
}

type nativeCandidateFile struct {
	Kind       string
	Executable bool
	Digest     string
}

// NativeCandidateFileState hashes regular bytes and preserves unchanged symlink identity.
func nativeCandidateFileState(root, name string, strict bool) (*nativeCandidateFile, error) {
	target := filepath.Join(root, name)
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !strict && info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		return &nativeCandidateFile{Kind: "symlink", Digest: link}, err
	}
	links, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || (strict && (!ok || links.Nlink != 1)) {
		return nil, fmt.Errorf("candidate output is not a singly linked regular file: %s", name)
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return nil, err
	}
	return &nativeCandidateFile{
		Kind:       "file",
		Executable: info.Mode().Perm()&0o111 != 0,
		Digest:     fmt.Sprintf("%x", digest.Sum(nil)),
	}, nil
}

// State includes all tracked names without applying changed-path restrictions.
func (v *nativeCandidate) state(ctx context.Context) (map[string]*nativeCandidateFile, error) {
	tracked, err := v.git(ctx, "", "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	untracked, err := v.untracked(ctx)
	if err != nil {
		return nil, err
	}
	state := map[string]*nativeCandidateFile{}
	for _, name := range append(nativeCandidateNames(tracked), untracked...) {
		state[name], err = nativeCandidateFileState(v.root, name, false)
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}

// Clean refuses late tracked, ordinary untracked, and ignored vendor output.
func (v *nativeCandidate) clean(ctx context.Context) error {
	status, err := v.git(ctx, "", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	untracked, err := v.untracked(ctx)
	if err != nil {
		return err
	}
	if status != "" || len(untracked) != 0 {
		return errors.New("validation left candidate dirty; inspect full logs and git status (no request prepared)")
	}
	return nil
}

// Snapshot checks an isolated index without mutating the caller's real index.
func (v *nativeCandidate) snapshot(ctx context.Context, base string, group map[string]any, complete bool) (string, []string, error) {
	directory, err := os.MkdirTemp(v.attempt, "native-index-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(directory)
	index := filepath.Join(directory, "index")
	if _, err = v.git(ctx, index, "read-tree", "HEAD"); err != nil {
		return "", nil, err
	}
	if err = v.stage(ctx, base, index); err != nil {
		return "", nil, err
	}
	tree, err := v.git(ctx, index, "write-tree")
	if err != nil {
		return "", nil, err
	}
	names, err := v.git(ctx, "", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", base, tree)
	if err != nil {
		return "", nil, err
	}
	changed := nativeCandidateNames(names)
	for _, name := range changed {
		if !safeNativePath(name) {
			return "", nil, fmt.Errorf("unsafe candidate path: %s", name)
		}
	}
	err = NativeValidateTree(ctx, v.read, v.root, base, tree, changed, group, complete)
	return tree, changed, err
}

// NativeCandidateTargets discovers explicit Make targets, never recipes or command text.
func nativeCandidateTargets(text string) map[string]bool {
	set := map[string]bool{}
	for _, match := range nativeMakeTarget.FindAllStringSubmatch(text, -1) {
		for _, name := range strings.Fields(match[1]) {
			set[name] = true
		}
	}
	return set
}

// Plan selects only fixed repository consumers using the inventory base's conventions.
func (v *nativeCandidate) plan(ctx context.Context, base string, group map[string]any, manifests []string, compiler string) ([]nativeCandidateCommand, error) {
	listing, err := v.git(ctx, "", "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, name := range nativeCandidateNames(listing) {
		available[name] = true
	}
	var plan []nativeCandidateCommand
	switch group["ecosystem"] {
	case "npm":
		roots := map[string]bool{}
		for _, name := range manifests {
			roots[path.Dir(name)] = true
		}
		for _, directory := range nativeCandidateSorted(roots) {
			prefix := directory + "/"
			if directory == "." {
				prefix = ""
			}
			if !available[prefix+"package.json"] {
				return nil, fmt.Errorf("unsupported npm consumer: missing package.json in %s", directory)
			}
			if !available[prefix+"package-lock.json"] && !available[prefix+"npm-shrinkwrap.json"] {
				return nil, fmt.Errorf("unsupported npm consumer: no committed lock in %s", directory)
			}
			if !available[prefix+"Makefile"] {
				return nil, fmt.Errorf("unsupported npm consumer: no repository Makefile in %s", directory)
			}
			text, err := v.git(ctx, "", "show", base+":"+prefix+"Makefile")
			if err != nil {
				return nil, err
			}
			targets := []string{"fmt", "lint", "test"}
			if directory == "api" {
				targets = nativeAPITargets
			}
			missing := map[string]bool{}
			explicit := nativeCandidateTargets(text)
			for _, target := range targets {
				if !explicit[target] {
					missing[target] = true
				}
			}
			if len(missing) > 0 {
				return nil, fmt.Errorf("unsupported npm consumer %s: missing fixed Make consumers %s", directory, strings.Join(nativeCandidateSorted(missing), ", "))
			}
			name := "npm-" + strings.ReplaceAll(directory, "/", "-")
			plan = append(plan, nativeCandidateCommand{name: name, argv: []string{"npm", "ci"}, directory: directory})
			for _, target := range targets {
				plan = append(plan, nativeCandidateCommand{name: name + "-" + target, argv: []string{"make", "-C", directory, target}, directory: "."})
			}
		}
	case "go":
		for _, name := range manifests {
			if path.Base(name) != "go.mod" {
				return nil, errors.New("unsupported Go source manifest; expected go.mod consumers")
			}
		}
		if !available["go.work"] || !available["image-sync/oc-mirror/acrauth/go.mod"] {
			return nil, errors.New("unsupported Go workspace: missing workspace or independent acrauth consumer")
		}
		text, err := v.git(ctx, "", "show", base+":Makefile")
		if err != nil {
			return nil, err
		}
		targets := nativeCandidateTargets(text)
		missing := map[string]bool{}
		for _, name := range []string{"all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify"} {
			if !targets[name] {
				missing[name] = true
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("unsupported Go workspace: missing fixed Make consumers %s", strings.Join(nativeCandidateSorted(missing), ", "))
		}
		for _, target := range []string{"verify-vendor", "test-compile", "test-unit", "lint", "verify"} {
			plan = append(plan, nativeCandidateCommand{name: "go-" + target, argv: []string{"make", target}, directory: "."})
		}
	default:
		if compiler == "" {
			return nil, errors.New("actions validation requires --compiler PATH to the repository-pinned compiler")
		}
		compiler, err = filepath.Abs(compiler)
		if err != nil {
			return nil, err
		}
		compiler, err = filepath.EvalSymlinks(compiler)
		if err != nil {
			return nil, errors.New("actions compiler is not executable")
		}
		info, err := os.Stat(compiler)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil, errors.New("actions compiler is not executable")
		}
		affected := map[string]bool{}
		changed, err := v.paths(ctx, base)
		if err != nil {
			return nil, err
		}
		for _, name := range append(append([]string{}, manifests...), changed...) {
			affected[name] = true
		}
		sources, yamlFiles := map[string]bool{}, map[string]bool{}
		for name := range affected {
			switch {
			case strings.HasPrefix(name, ".github/workflows/") && strings.HasSuffix(name, ".md"):
				sources[name] = true
			case strings.HasSuffix(name, ".lock.yml"):
				source := strings.TrimSuffix(name, ".lock.yml") + ".md"
				if !available[source] {
					return nil, fmt.Errorf("unsupported Actions consumer: generated lock has no live source: %s", name)
				}
				sources[source] = true
			case strings.HasPrefix(name, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) && !available[name]:
				stem := strings.TrimSuffix(name, path.Ext(name))
				if !available[stem+".md"] || !available[stem+".lock.yml"] {
					return nil, fmt.Errorf("unsupported Actions consumer: obsolete workflow has no live source: %s", name)
				}
				sources[stem+".md"] = true
			}
			if available[name] && strings.HasPrefix(name, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
				yamlFiles[name] = true
			}
		}
		if affected[".github/aw/actions-lock.json"] {
			for name := range available {
				if strings.HasPrefix(name, ".github/workflows/") && strings.HasSuffix(name, ".md") {
					sources[name] = true
				}
			}
		}
		for _, source := range nativeCandidateSorted(sources) {
			stem := strings.TrimSuffix(path.Base(source), ".md")
			plan = append(plan, nativeCandidateCommand{name: "actions-" + stem, argv: []string{compiler, "compile", stem, "--strict"}, directory: "."})
			yamlFiles[strings.TrimSuffix(source, ".md")+".lock.yml"] = true
		}
		if len(yamlFiles) == 0 {
			return nil, errors.New("unsupported Actions consumer: no live YAML workflow to validate")
		}
		if !available[".yamllint.yml"] {
			return nil, errors.New("unsupported Actions consumer: missing repository yamllint policy")
		}
		plan = append(plan, nativeCandidateCommand{name: "actions-yaml-lint", argv: append([]string{"yamllint", "-c", ".yamllint.yml"}, nativeCandidateSorted(yamlFiles)...), directory: "."})
	}
	return plan, nil
}

var nativeCandidateSDK = regexp.MustCompile(`^test/sdk/v[0-9]+(?:preview)?/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/.+$`)

// NativeCandidateGenerated identifies baseline-comparable generated API output.
func nativeCandidateGenerated(name string) bool {
	return strings.HasPrefix(name, "internal/azureapi/") && strings.Contains(name, "/generated/") && strings.HasSuffix(name, ".go") ||
		nativeCandidateSDK.MatchString(name) ||
		strings.HasPrefix(name, "api/redhatopenshift/") && (strings.HasSuffix(name, ".tsp") || strings.HasSuffix(name, "/openapi.json"))
}

// Baseline measures generated drift in an isolated inventory-base checkout.
func (v *nativeCandidate) baseline(ctx context.Context, base string, initial map[string]*nativeCandidateFile, manifests []string) (drift map[string]*nativeCandidateFile, err error) {
	directory, err := os.MkdirTemp(v.attempt, "native-base-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	checkout := filepath.Join(directory, "checkout")
	if _, err = v.git(ctx, "", "worktree", "add", "--detach", checkout, base); err != nil {
		return nil, err
	}
	defer func() {
		_, cleanupErr := v.git(context.WithoutCancel(ctx), "", "worktree", "remove", "--force", checkout)
		err = errors.Join(err, cleanupErr)
	}()
	baseline := &nativeCandidate{root: checkout, repo: &repository.Repo{Path: checkout}}
	before, err := baseline.state(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = v.run(ctx, nativeCandidateCommand{name: "base-npm-api", argv: []string{"npm", "ci"}, directory: "api"}, checkout); err != nil {
		return nil, err
	}
	for _, target := range nativeAPIGenerators {
		if _, err = v.run(ctx, nativeCandidateCommand{name: "base-api-" + target, argv: []string{"make", "-C", "api", target}, directory: "."}, checkout); err != nil {
			return nil, err
		}
	}
	after, err := baseline.state(ctx)
	if err != nil {
		return nil, err
	}
	names, manifestSet := map[string]bool{}, map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	for _, name := range manifests {
		manifestSet[name] = true
	}
	drift = map[string]*nativeCandidateFile{}
	for name := range names {
		if !reflect.DeepEqual(before[name], after[name]) && nativeCandidateGenerated(name) && !manifestSet[name] && reflect.DeepEqual(initial[name], before[name]) {
			drift[name] = after[name]
		}
	}
	return drift, nil
}

// ExcludeBaseline discards only exact generated drift on initially unchanged files.
func (v *nativeCandidate) excludeBaseline(ctx context.Context, base string, drift map[string]*nativeCandidateFile) error {
	excluded := []string{}
	names := map[string]bool{}
	for name := range drift {
		names[name] = true
	}
	for _, name := range nativeCandidateSorted(names) {
		state, err := nativeCandidateFileState(v.root, name, true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(state, drift[name]) {
			continue
		}
		entry, err := v.git(ctx, "", "ls-tree", base, "--", name)
		if err != nil {
			return err
		}
		if entry != "" {
			if _, err = v.git(ctx, "", "restore", "--source="+base, "--worktree", "--", name); err != nil {
				return err
			}
		} else if err = os.Remove(filepath.Join(v.root, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		excluded = append(excluded, name)
	}
	v.results["excluded_baseline_identical_generation"] = excluded
	return v.save()
}

// Workspace discovers modules through Go's parser and validates relative paths.
func (v *nativeCandidate) workspace(ctx context.Context, name string) ([]string, error) {
	log, err := v.run(ctx, nativeCandidateCommand{name: name, argv: []string{"go", "work", "edit", "-json"}, directory: "."}, "")
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(log)
	if err != nil {
		return nil, err
	}
	var document struct{ Use []struct{ DiskPath string } }
	if err = json.Unmarshal(content, &document); err != nil {
		return nil, err
	}
	if len(document.Use) == 0 {
		return nil, errors.New("go workspace discovery returned no modules")
	}
	var modules []string
	for _, entry := range document.Use {
		if !strings.HasPrefix(entry.DiskPath, "./") || !safeNativePath(strings.TrimPrefix(entry.DiskPath, "./")) {
			return nil, errors.New("unsupported Go workspace module path")
		}
		directory := strings.TrimPrefix(entry.DiskPath, "./")
		info, err := os.Stat(filepath.Join(v.root, directory, "go.mod"))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("missing Go workspace module: %s", directory)
		}
		modules = append(modules, directory)
	}
	return modules, nil
}

// GeneratedGo tests generated modules and their root vendored consumers.
func (v *nativeCandidate) generatedGo(ctx context.Context, changed []string, tree string) error {
	internal, sdk := false, false
	var sdkGo []string
	for _, name := range changed {
		internal = internal || strings.HasPrefix(name, "internal/azureapi/") && strings.HasSuffix(name, ".go")
		if nativeCandidateSDK.MatchString(name) {
			sdk = true
			if strings.HasSuffix(name, ".go") || path.Base(name) == "go.mod" || path.Base(name) == "go.sum" {
				sdkGo = append(sdkGo, name)
			}
		}
	}
	if !internal && !sdk {
		return nil
	}
	workspace := map[string]bool{}
	if internal || len(sdkGo) > 0 {
		modules, err := v.workspace(ctx, "npm-generated-workspace")
		if err != nil {
			return err
		}
		for _, module := range modules {
			workspace[module] = true
		}
	}
	if internal {
		if !workspace["internal"] {
			return errors.New("unsupported generated API consumer: internal is outside the workspace")
		}
		if _, err := v.run(ctx, nativeCandidateCommand{name: "npm-generated-internal-test", argv: []string{"go", "test", "-timeout", "1200s", "./internal/azureapi/..."}, directory: "."}, ""); err != nil {
			return err
		}
		if err := v.clean(ctx); err != nil {
			return err
		}
	}
	listing, err := v.git(ctx, "", "ls-tree", "-r", "--name-only", "-z", tree)
	if err != nil {
		return err
	}
	available, modules := map[string]bool{}, map[string]bool{}
	for _, name := range nativeCandidateNames(listing) {
		available[name] = true
	}
	for _, name := range sdkGo {
		directory := path.Dir(name)
		for directory != "." && !available[directory+"/go.mod"] {
			directory = path.Dir(directory)
		}
		if !nativeCandidateSDK.MatchString(directory + "/go.mod") {
			return fmt.Errorf("unsupported generated SDK consumer: no generated module boundary for %s", name)
		}
		modules[directory] = true
	}
	for index, directory := range nativeCandidateSorted(modules) {
		command := nativeCandidateCommand{name: fmt.Sprintf("npm-generated-sdk-test-%d", index+1), argv: []string{"go", "test", "-mod=readonly", "-timeout", "1200s", "./..."}, directory: directory}
		if !workspace[directory] {
			command.environment = []string{"GOWORK=off"}
		}
		if _, err := v.run(ctx, command, ""); err != nil {
			return err
		}
		if err := v.clean(ctx); err != nil {
			return err
		}
	}
	if sdk {
		if _, err := v.run(ctx, nativeCandidateCommand{name: "npm-generated-verify-vendor", argv: []string{"make", "verify-vendor"}, directory: "."}, ""); err != nil {
			return err
		}
		return v.clean(ctx)
	}
	return nil
}

// NativeCandidateQuote renders evidence words without ever executing a shell.
func nativeCandidateQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n'\"`$;&|()<>\\*?[]{}!~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Body appends complete local evidence without changing the original body file.
func (v *nativeCandidate) body(proposed, base, head string) (string, error) {
	if len(v.steps) == 0 {
		return "", errors.New("cannot record validation evidence for incomplete consumers")
	}
	content, err := os.ReadFile(proposed)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	text.WriteString(strings.TrimRight(string(content), " \t\r\n"))
	fmt.Fprintf(&text, "\n\n### Repository-owned candidate validation\n\n- Inventory base: `%s`\n- Validated local candidate: `%s`\n- Dependency scope and complete source coverage: passed.\n- These are local consumer results, not source-PR CI or deployed-state evidence.\n- Full per-step logs and machine results are retained in the validation artifacts.\n\n", base, head)
	for _, step := range v.steps {
		if step["status"] != "passed" || step["returncode"] != 0 {
			return "", errors.New("cannot record validation evidence for incomplete consumers")
		}
		var arguments []string
		environment := step["environment"].(map[string]string)
		keys := map[string]bool{}
		for key := range environment {
			keys[key] = true
		}
		for _, key := range nativeCandidateSorted(keys) {
			arguments = append(arguments, nativeCandidateQuote(key+"="+environment[key]))
		}
		for _, argument := range step["argv"].([]string) {
			arguments = append(arguments, nativeCandidateQuote(argument))
		}
		fmt.Fprintf(&text, "- **%s**: passed (exit 0), `%s`\n", step["name"], strings.ReplaceAll(strings.Join(arguments, " "), "`", "\\`"))
	}
	if excluded, ok := v.results["excluded_baseline_identical_generation"].([]string); ok && len(excluded) > 0 {
		text.WriteString("\nExcluded only initially unchanged generated files whose bytes exactly matched the isolated inventory-base generation:\n")
		for _, name := range excluded {
			fmt.Fprintf(&text, "- `%s`\n", name)
		}
	}
	textValue, err := nativeSafeText(text.String(), false)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(v.attempt, "native-validated-body.txt")
	return destination, os.WriteFile(destination, []byte(textValue), 0o600)
}

// Expect checks exact Git identities at phase boundaries.
func (v *nativeCandidate) expect(ctx context.Context, expected, message string, args ...string) error {
	actual, err := v.git(ctx, "", args...)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New(message)
	}
	return nil
}

// NativeCandidateInputsEqual detects edits or removal of immutable request inputs.
func nativeCandidateInputsEqual(inputs map[string][]byte) bool {
	for name, expected := range inputs {
		current, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(expected, current) {
			return false
		}
	}
	return true
}

// Validate performs preflight, generation, committed checks, and guarded request handoff.
func (v *nativeCandidate) validate(ctx context.Context, options NativeCandidateOptions) (result map[string]string, err error) {
	defer func() {
		if err != nil {
			cleanupErr := os.Remove(filepath.Join(v.artifacts, nativeRequestFile))
			if cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				err = errors.Join(err, cleanupErr)
			}
			v.results["status"], v.results["error"] = "failed", err.Error()
			err = errors.Join(err, v.save())
		}
	}()
	inputs := map[string][]byte{}
	for _, name := range []string{options.Inventory, options.TitleFile, options.BodyFile} {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return nil, err
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		inputs[absolute] = content
	}
	inventory, err := os.Open(options.Inventory)
	if err != nil {
		return nil, err
	}
	selected, err := SelectNativeInventoryGroup(inventory, options.Repository, options.Group, options.Target)
	inventory.Close()
	if err != nil {
		return nil, err
	}
	var group map[string]any
	decoder := json.NewDecoder(bytes.NewReader(selected.Group))
	decoder.UseNumber()
	if err := decoder.Decode(&group); err != nil {
		return nil, err
	}
	base := selected.BaseSHA
	for key, value := range map[string]any{"base_sha": base, "group": options.Group, "target": options.Target, "ecosystem": group["ecosystem"], "family": group["family"], "manifests": selected.Manifests} {
		v.results[key] = value
	}
	if err := v.save(); err != nil {
		return nil, err
	}
	if err := v.expect(ctx, base, "inventory base commit is unavailable", "rev-parse", "--verify", base+"^{commit}"); err != nil {
		return nil, err
	}
	if err := v.expect(ctx, base, "candidate HEAD must descend from the inventory base; do not use a native PR checkout", "merge-base", base, "HEAD"); err != nil {
		return nil, err
	}
	staged, err := v.git(ctx, "", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	unstaged, err := v.git(ctx, "", "diff", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	stagedSet := map[string]bool{}
	for _, name := range nativeCandidateNames(staged) {
		stagedSet[name] = true
	}
	for _, name := range nativeCandidateNames(unstaged) {
		if stagedSet[name] {
			return nil, errors.New("partially staged candidate paths; preserve or commit them before validation")
		}
	}
	initial, err := v.state(ctx)
	if err != nil {
		return nil, err
	}
	if _, _, err = v.snapshot(ctx, base, group, false); err != nil {
		return nil, err
	}
	plan, err := v.plan(ctx, base, group, selected.Manifests, options.Compiler)
	if err != nil {
		return nil, err
	}
	var drift map[string]*nativeCandidateFile
	if group["ecosystem"] == "npm" {
		for _, name := range selected.Manifests {
			if path.Dir(name) == "api" {
				drift, err = v.baseline(ctx, base, initial, selected.Manifests)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	if group["ecosystem"] == "go" {
		if _, err = v.run(ctx, nativeCandidateCommand{name: "go-all-tidy", argv: []string{"make", "all-tidy"}, directory: "."}, ""); err != nil {
			return nil, err
		}
		first, err := v.state(ctx)
		if err != nil {
			return nil, err
		}
		if _, err = v.run(ctx, nativeCandidateCommand{name: "go-all-tidy-fixpoint", argv: []string{"make", "all-tidy"}, directory: "."}, ""); err != nil {
			return nil, err
		}
		second, err := v.state(ctx)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(first, second) {
			return nil, errors.New("make all-tidy did not reach a second-run fixpoint")
		}
		if _, err = v.run(ctx, nativeCandidateCommand{name: "go-vendor", argv: []string{"make", "vendor"}, directory: "."}, ""); err != nil {
			return nil, err
		}
	} else {
		for _, command := range plan {
			if _, err = v.run(ctx, command, ""); err != nil {
				return nil, err
			}
		}
	}
	if len(drift) > 0 {
		if err = v.excludeBaseline(ctx, base, drift); err != nil {
			return nil, err
		}
	}
	tree, changed, err := v.snapshot(ctx, base, group, true)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, errors.New("candidate has no dependency changes")
	}
	if err = v.stage(ctx, base, ""); err != nil {
		return nil, err
	}
	if err = v.expect(ctx, tree, "candidate changed while staging validated scope", "write-tree"); err != nil {
		return nil, err
	}
	staged, err = v.git(ctx, "", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	if staged != "" {
		if _, err = v.git(ctx, "", "commit", "-m", "chore: validate native dependency candidate"); err != nil {
			return nil, err
		}
	}
	if err = v.clean(ctx); err != nil {
		return nil, err
	}
	head, err := v.git(ctx, "", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if err = v.expect(ctx, tree, "candidate commit differs from validated tree", "rev-parse", "HEAD^{tree}"); err != nil {
		return nil, err
	}
	switch group["ecosystem"] {
	case "go":
		for _, command := range plan {
			if _, err = v.run(ctx, command, ""); err != nil {
				return nil, err
			}
			if err = v.clean(ctx); err != nil {
				return nil, err
			}
		}
		modules, err := v.workspace(ctx, "go-workspace")
		if err != nil {
			return nil, err
		}
		args := []string{"go", "build"}
		for _, module := range modules {
			args = append(args, "./"+module+"/...")
		}
		if _, err = v.run(ctx, nativeCandidateCommand{name: "go-build", argv: args, directory: "."}, ""); err != nil {
			return nil, err
		}
		for _, command := range []nativeCandidateCommand{
			{name: "go-acrauth-build", argv: []string{"go", "build", "./..."}, directory: "image-sync/oc-mirror/acrauth", environment: []string{"GOWORK=off"}},
			{name: "go-acrauth-test", argv: []string{"go", "test", "-timeout", "1200s", "./..."}, directory: "image-sync/oc-mirror/acrauth", environment: []string{"GOWORK=off"}},
		} {
			if _, err = v.run(ctx, command, ""); err != nil {
				return nil, err
			}
		}
		if err = v.clean(ctx); err != nil {
			return nil, err
		}
	case "npm":
		if err = v.generatedGo(ctx, changed, tree); err != nil {
			return nil, err
		}
	}
	for _, command := range []nativeCandidateCommand{
		{name: "common-1", argv: []string{"make", "verify-yamlfmt"}, directory: "."},
		{name: "common-2", argv: []string{"make", "-C", "config", "detect-change"}, directory: "."},
	} {
		if _, err = v.run(ctx, command, ""); err != nil {
			return nil, err
		}
		if err = v.clean(ctx); err != nil {
			return nil, err
		}
	}
	if err = v.expect(ctx, head, "candidate HEAD changed during validation", "rev-parse", "HEAD"); err != nil {
		return nil, err
	}
	if err = v.expect(ctx, tree, "candidate tree changed during validation", "rev-parse", "HEAD^{tree}"); err != nil {
		return nil, err
	}
	if !nativeCandidateInputsEqual(inputs) {
		return nil, errors.New("inventory or publication text changed during validation")
	}
	if err = NativeValidateTree(ctx, v.read, v.root, base, tree, changed, group, true); err != nil {
		return nil, err
	}
	if err = v.clean(ctx); err != nil {
		return nil, err
	}
	body, err := v.body(options.BodyFile, base, head)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := os.ReadFile(body)
	if err != nil {
		return nil, err
	}
	result, err = v.prepare(ctx, NativePrepareOptions{Repository: options.Repository, Root: v.root, Inventory: options.Inventory, Group: options.Group, Target: options.Target, TitleFile: options.TitleFile, BodyFile: body, Artifacts: v.artifacts})
	if err != nil {
		return nil, err
	}
	if err = v.clean(ctx); err != nil {
		return nil, err
	}
	current, err := v.git(ctx, "", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	latestBody, err := os.ReadFile(body)
	if err != nil {
		return nil, err
	}
	if current != head || !bytes.Equal(bodyBytes, latestBody) || !nativeCandidateInputsEqual(inputs) {
		return nil, errors.New("candidate or request inputs changed during request construction")
	}
	v.results["status"], v.results["candidate_head_sha"], v.results["candidate_tree"] = "passed", head, tree
	if err = v.save(); err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateNativeCandidate validates fixed native consumers before preparing an offline request.
func ValidateNativeCandidate(ctx context.Context, read NativeReadFunc, options NativeCandidateOptions) (result map[string]string, err error) {
	root := options.Root
	if root == "" {
		root = "."
	}
	repo := &repository.Repo{Path: root}
	top, err := repo.NativeGit(ctx, nil, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	v, err := newNativeCandidate(strings.TrimSpace(string(top)), options.Artifacts, read)
	if err != nil {
		return nil, err
	}
	return v.validate(ctx, options)
}
