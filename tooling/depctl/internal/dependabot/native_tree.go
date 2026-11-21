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
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

var nativeSDKGo = regexp.MustCompile(`^1\.[0-9]+(?:\.[0-9]+)?$`)
var nativeSDKChecksum = regexp.MustCompile(`^\S+ v[0-9]\S* h1:[A-Za-z0-9+/]+=*$`)
var nativeVendorVersion = regexp.MustCompile(`(?m)^# (\S+) (v[0-9]\S*)(?: => .*)?$`)
var nativeVendors = []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"}

// Native vendor paths match only the established generated consumers.
func nativeVendorPath(filename string) bool {
	for _, prefix := range nativeVendors {
		if strings.HasPrefix(filename, prefix) {
			return true
		}
	}
	return false
}

// NativeValidateTree checks exact paths, regular file modes, scope and consumer coverage.
func NativeValidateTree(ctx context.Context, read NativeReadFunc, root, base, tree string, paths []string, group map[string]any, complete bool) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !nativeSHA.MatchString(base) || !nativeSHA.MatchString(tree) {
		return nativeReject("tree validation requires a repository root and full lowercase base/tree SHAs")
	}
	ecosystem, _ := group["ecosystem"].(string)
	family, _ := group["family"].(string)
	if err := nativeValidateTreeMetadata(group); err != nil {
		return err
	}
	for _, filename := range paths {
		if !safeNativePath(filename) {
			return nativeReject("unsafe replacement path")
		}
	}
	if err := nativeValidateReplacementPaths(paths, nativeInventoryGroup{Ecosystem: ecosystem, Family: family}); err != nil {
		return err
	}
	repo := &repository.Repo{Path: root}
	git := func(args ...string) (string, error) {
		value, err := repo.NativeGit(ctx, nil, "", append([]string{"-c", "core.quotepath=false"}, args...)...)
		return strings.TrimRight(string(value), "\n"), err
	}
	actual, err := git("diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", base, tree)
	if err != nil {
		return err
	}
	actualPaths := []string{}
	if actual != "" {
		actualPaths = strings.Split(actual, "\n")
	}
	expected := append([]string{}, paths...)
	sort.Strings(expected)
	sort.Strings(actualPaths)
	if !reflect.DeepEqual(actualPaths, expected) {
		return nativeReject("patch path list does not match resulting tree")
	}
	reader := func(filename string) (string, error) { return git("show", tree+":"+filename) }
	baseReader := func(filename string) (string, error) { return git("show", base+":"+filename) }
	manifests := []map[string]any{}
	for _, filename := range paths {
		var modes [2]string
		for i, ref := range []string{base, tree} {
			listing, err := git("ls-tree", ref, "--", filename)
			if err != nil {
				return err
			}
			if listing != "" {
				modes[i] = strings.SplitN(listing, " ", 2)[0]
			}
		}
		generated := ecosystem == "go" && nativeVendorPath(filename) || ecosystem == "npm" && family == "typespec" && (strings.HasPrefix(filename, "internal/azureapi/") && strings.Contains(filename, "/generated/") || nativeSDKConsumer.MatchString(filename))
		if (modes[0] != "" && modes[0] != "100644") || (modes[1] != "" && modes[1] != "100644") || modes[0] == "" && modes[1] == "" || !generated && (modes[0] == "" || modes[1] == "") {
			return nativeReject("only regular files; additions/removals limited to generated consumers")
		}
		name := path.Base(filename)
		if filename == ".github/aw/actions-lock.json" {
			before, err := baseReader(filename)
			if err != nil {
				return err
			}
			after, err := reader(filename)
			if err != nil {
				return err
			}
			if err := nativeActionLockChanges(ctx, read, before, after, group); err != nil {
				return err
			}
		} else if ecosystem == "npm" && family == "typespec" && nativeSDKConsumer.MatchString(filename) && name == "go.mod" {
			if modes[1] == "" {
				return nativeReject("generated SDK module metadata cannot be deleted")
			}
			before := ""
			if modes[0] != "" {
				before, err = baseReader(filename)
				if err != nil {
					return err
				}
			}
			after, err := reader(filename)
			if err != nil {
				return err
			}
			workspace, err := reader("go.work")
			if err != nil {
				return err
			}
			matches := regexp.MustCompile(`(?m)^go (1\.[0-9]+(?:\.[0-9]+)?)$`).FindAllStringSubmatch(workspace, -1)
			if len(matches) != 1 {
				return nativeReject("missing workspace Go toolchain")
			}
			if err := nativeValidateSDKModule(filename, before, after, matches[0][1]); err != nil {
				return err
			}
		} else if ecosystem == "npm" && nativeSDKConsumer.MatchString(filename) && name == "go.sum" && modes[1] != "" {
			text, err := reader(filename)
			if err != nil {
				return err
			}
			for _, line := range nativeLines(text) {
				if !nativeSDKChecksum.MatchString(line) {
					return nativeReject("malformed generated SDK checksum metadata")
				}
			}
		} else if name == "go.mod" || name == "package.json" || nativeLockPath(filename) || ecosystem == "actions" {
			before, err := baseReader(filename)
			if err != nil {
				return err
			}
			after, err := reader(filename)
			if err != nil {
				return err
			}
			entries, err := nativeChanges(ctx, read, filename, before, after)
			if err != nil {
				return err
			}
			for _, item := range entries {
				belongs := nativeUpgradeFamily(ecosystem, item["package"].(string)) == family
				if ecosystem == "go" && belongs && (item["target"] == nil || item["old"] != nil && !nativeVersionCovers(item["target"], item["old"])) {
					return nativeReject("Go replacement downgrades/removes a preserved current-base family requirement")
				}
				if (ecosystem == "actions" || ecosystem == "npm") && !nativeLockPath(filename) && !belongs {
					return nativeReject("replacement changes an unrelated dependency family")
				}
			}
			manifests = append(manifests, entries...)
		}
	}
	found := false
	for _, item := range manifests {
		found = found || nativeUpgradeFamily(ecosystem, item["package"].(string)) == family
	}
	if !found {
		return nativeReject("replacement lacks the actual source upgrade family/target")
	}
	baseListing, err := git("ls-tree", "-r", "--name-only", base)
	if err != nil {
		return err
	}
	baseFiles := nativePathSet(baseListing)
	if ecosystem == "npm" {
		roots := map[string]bool{}
		for _, item := range manifests {
			roots[path.Dir(item["manifest"].(string))] = true
		}
		for _, filename := range paths {
			if (path.Base(filename) == "package.json" || nativeLockPath(filename)) && !roots[path.Dir(filename)] {
				return nativeReject("npm patch changes unrelated consumer roots")
			}
		}
		for root := range roots {
			lock := path.Join(root, "package-lock.json")
			manifest := path.Join(root, "package.json")
			if !baseFiles[lock] {
				continue
			}
			before, err := baseReader(lock)
			if err != nil {
				return err
			}
			after, err := reader(lock)
			if err != nil {
				return err
			}
			old, err := nativeReadLock(before)
			if err != nil {
				return err
			}
			current, err := nativeReadLock(after)
			if err != nil {
				return err
			}
			if err := nativePreserveNPMResolutions(old, current); err != nil {
				return err
			}
			if baseFiles[manifest] {
				text, err := reader(manifest)
				if err != nil {
					return err
				}
				if err := nativeValidateNPMOverrides(text, current); err != nil {
					return err
				}
			}
		}
	}
	listing, err := git("ls-tree", "-r", "--name-only", tree)
	if err != nil {
		return err
	}
	available := nativePathSet(listing)
	if complete {
		if ecosystem == "go" {
			if err := nativeGoConsumerCoverage(group, available, baseFiles, reader, baseReader); err != nil {
				return err
			}
		}
		for _, raw := range group["sources"].([]any) {
			source := raw.(map[string]any)
			intents, err := nativeRowsValue(source["intents"])
			if err != nil {
				return err
			}
			if err := NativeVerifyCoverage(ctx, read, intents, reader, available); err != nil {
				return err
			}
		}
	}
	return nil
}

// Native path sets retain exact Git tree filenames.
func nativePathSet(listing string) map[string]bool {
	result := map[string]bool{}
	for _, filename := range strings.Split(listing, "\n") {
		if filename != "" {
			result[filename] = true
		}
	}
	return result
}

// Native SDK modules accept only generated identity, supported Go and bounded dependency changes.
func nativeValidateSDKModule(filename, before, after, workspace string) error {
	parse := func(text string) (nativeVersionSet, [3]int, error) {
		var goVersion [3]int
		moduleCount, goCount := 0, 0
		inRequire := false
		for _, raw := range strings.Split(text, "\n") {
			line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
			if line == "" {
				continue
			}
			switch {
			case strings.HasPrefix(line, "module "):
				moduleCount++
				if strings.TrimSpace(strings.TrimPrefix(line, "module ")) != "github.com/Azure/ARO-HCP/"+path.Dir(filename) {
					return nil, goVersion, nativeReject("SDK generated module identity is not exact")
				}
			case strings.HasPrefix(line, "go "):
				goCount++
				version := strings.TrimSpace(strings.TrimPrefix(line, "go "))
				if !nativeSDKGo.MatchString(version) {
					return nil, goVersion, nativeReject("unsupported generated SDK Go version")
				}
				goVersion = nativeGoRelease(version)
			case line == "require (":
				if inRequire {
					return nil, goVersion, nativeReject("nested SDK requirements")
				}
				inRequire = true
			case line == ")":
				if !inRequire {
					return nil, goVersion, nativeReject("unmatched SDK requirement block")
				}
				inRequire = false
			default:
				if !nativeGoEntry.MatchString(line) || !inRequire && !strings.HasPrefix(line, "require ") {
					return nil, goVersion, nativeReject("SDK module contains unsupported source/toolchain/replace configuration")
				}
			}
		}
		if inRequire || moduleCount != 1 || goCount != 1 {
			return nil, goVersion, nativeReject("SDK generated module identity is not exact")
		}
		versions, err := nativeGoVersions(text)
		return versions, goVersion, err
	}
	current, version, err := parse(after)
	if err != nil {
		return err
	}
	if nativeReleaseCompare(version, nativeGoRelease(workspace)) > 0 {
		return nativeReject("SDK generator requires an unvalidated newer Go toolchain")
	}
	old := nativeVersionSet{}
	if before != "" {
		var oldVersion [3]int
		old, oldVersion, err = parse(before)
		if err != nil {
			return err
		}
		if nativeReleaseCompare(version, oldVersion) < 0 {
			return nativeReject("SDK generator downgrades its Go requirement")
		}
	}
	allowed := map[string]bool{"github.com/Azure/azure-sdk-for-go/sdk/azcore": true, "github.com/Azure/azure-sdk-for-go/sdk/internal": true, "golang.org/x/net": true, "golang.org/x/text": true}
	for key, item := range current {
		if !allowed[item.Package] && old[key].Version != item.Version {
			return nativeReject("SDK metadata introduces an unrelated dependency upgrade")
		}
	}
	for key, item := range old {
		if next, ok := current[key]; ok && !nativeVersionCovers(next.Version, item.Version) {
			return nativeReject("SDK metadata downgrades an existing dependency")
		}
	}
	return nil
}

// Native Go releases are numeric three-component toolchain requirements.
func nativeGoRelease(value string) [3]int {
	var result [3]int
	for i, part := range strings.Split(value, ".") {
		if i < 3 {
			result[i], _ = strconv.Atoi(part)
		}
	}
	return result
}

// Native Go consumers preserve selected family requirements in modules and vendors.
func nativeGoConsumerCoverage(group map[string]any, available, baseFiles map[string]bool, reader, baseReader func(string) (string, error)) error {
	targets := map[string]any{}
	for _, raw := range group["sources"].([]any) {
		source := raw.(map[string]any)
		intents, err := nativeRowsValue(source["intents"])
		if err != nil {
			return err
		}
		for _, item := range intents {
			pkg := item["package"].(string)
			if item["target"] != nil && nativeUpgradeFamily("go", pkg) == group["family"] {
				targets[pkg] = item["target"]
			}
		}
	}
	for filename := range available {
		if strings.HasSuffix(filename, "go.mod") && !nativeVendorPath(filename) {
			text, err := reader(filename)
			if err != nil {
				return err
			}
			versions, err := nativeGoVersions(text)
			if err != nil {
				return err
			}
			for _, item := range versions {
				if target, ok := targets[item.Package]; ok && !nativeVersionCovers(item.Version, target) {
					return nativeReject("Go consumer still selects an older family version: " + filename)
				}
			}
		}
	}
	for _, prefix := range nativeVendors {
		present := false
		for filename := range available {
			present = present || strings.HasPrefix(filename, prefix)
		}
		if !present {
			continue
		}
		filename := prefix + "modules.txt"
		if !available[filename] {
			return nativeReject("vendor consumer lacks module provenance")
		}
		text, err := reader(filename)
		if err != nil {
			return err
		}
		versions := map[string]string{}
		for _, match := range nativeVendorVersion.FindAllStringSubmatch(text, -1) {
			versions[match[1]] = match[2]
		}
		for pkg, target := range targets {
			if actual, ok := versions[pkg]; ok && !nativeVersionCovers(actual, target) {
				return nativeReject("vendor consumer still selects an older family version: " + filename)
			}
		}
		if baseFiles[filename] {
			text, err := baseReader(filename)
			if err != nil {
				return err
			}
			for _, match := range nativeVendorVersion.FindAllStringSubmatch(text, -1) {
				if nativeUpgradeFamily("go", match[1]) == group["family"] && !nativeVersionCovers(versions[match[1]], match[2]) {
					return nativeReject("vendor replacement downgrades/removes a current-base family resolution")
				}
			}
		}
	}
	return nil
}

// Native compiler lock changes preserve every unrelated entry and metadata field.
func nativeActionLockChanges(ctx context.Context, read NativeReadFunc, before, after string, group map[string]any) error {
	leftValue, err := nativeDecode([]byte(before))
	if err != nil {
		return err
	}
	rightValue, err := nativeDecode([]byte(after))
	if err != nil {
		return err
	}
	left, err := nativeObject(leftValue)
	if err != nil {
		return err
	}
	right, err := nativeObject(rightValue)
	if err != nil {
		return err
	}
	old, ok := left["entries"].(map[string]any)
	if !ok {
		return nativeReject("invalid compiler actions lock")
	}
	current, ok := right["entries"].(map[string]any)
	if !ok {
		return nativeReject("invalid compiler actions lock")
	}
	delete(left, "entries")
	delete(right, "entries")
	if !reflect.DeepEqual(left, right) {
		return nativeReject("compiler containers or non-action lock fields changed")
	}
	allowed := map[string]bool{}
	sources, err := nativeRowsValue(group["sources"])
	if err != nil {
		return err
	}
	for _, source := range sources {
		intents, err := nativeRowsValue(source["intents"])
		if err != nil {
			return err
		}
		for _, item := range intents {
			if item["ecosystem"] == "actions" {
				allowed[nativeKey([]any{item["package"], item["target"]})] = true
			}
		}
	}
	keys := map[string]bool{}
	for key := range old {
		keys[key] = true
	}
	for key := range current {
		keys[key] = true
	}
	for key := range keys {
		if reflect.DeepEqual(old[key], current[key]) {
			continue
		}
		for _, raw := range []any{old[key], current[key]} {
			if raw == nil {
				continue
			}
			entry, ok := raw.(map[string]any)
			if !ok || len(entry) != 3 {
				return nativeReject("unrelated or malformed action lock entry")
			}
			pkg, pok := entry["repo"].(string)
			version, vok := entry["version"].(string)
			if !pok || !vok || key != pkg+"@"+version || nativeUpgradeFamily("actions", pkg) != group["family"] {
				return nativeReject("unrelated or malformed action lock entry")
			}
		}
		if raw, exists := current[key]; exists {
			entry := raw.(map[string]any)
			if !allowed[nativeKey([]any{entry["repo"], entry["sha"]})] {
				return nativeReject("lock pin is not the native intended upgrade")
			}
			value, err := nativeRead(ctx, read, "repos/"+nativeUpgradeFamily("actions", entry["repo"].(string))+"/commits/"+url.PathEscape(entry["version"].(string)), false, false)
			if err != nil {
				return err
			}
			official, err := nativeObject(value)
			if err != nil {
				return err
			}
			if official["sha"] != entry["sha"] {
				return nativeReject("action lock release is not the official intended commit")
			}
		}
	}
	return nil
}
