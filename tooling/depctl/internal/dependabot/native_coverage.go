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
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

var nativeNPMVersionPattern = regexp.MustCompile(`^([~^]?)(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Native version coverage requires valid complete Go semantic versions.
func nativeVersionCovers(actual, target any) bool {
	if actual == nil || target == nil {
		return actual == nil && target == nil
	}
	left, lok := actual.(string)
	right, rok := target.(string)
	return lok && rok && semver.IsValid(left) && semver.IsValid(right) && strings.Count(strings.SplitN(strings.SplitN(left, "-", 2)[0], "+", 2)[0], ".") == 2 && strings.Count(strings.SplitN(strings.SplitN(right, "-", 2)[0], "+", 2)[0], ".") == 2 && semver.Compare(left, right) >= 0
}

// Native npm versions retain the exact supported compatibility operator.
func nativeNPMVersion(value any) (string, [3]int, bool) {
	text, ok := value.(string)
	if !ok {
		return "", [3]int{}, false
	}
	match := nativeNPMVersionPattern.FindStringSubmatch(text)
	if match == nil {
		return "", [3]int{}, false
	}
	var version [3]int
	for i := range version {
		number, err := strconv.Atoi(match[i+2])
		if err != nil {
			return "", version, false
		}
		version[i] = number
	}
	return match[1], version, true
}

// Native version ordering compares numeric release components.
func nativeReleaseCompare(left, right [3]int) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// Native npm coverage preserves major, zero-minor and tilde compatibility.
func nativeNPMCovers(actual, target any, resolved bool) bool {
	op, left, lok := nativeNPMVersion(actual)
	targetOp, right, rok := nativeNPMVersion(target)
	if !lok || !rok || left[0] != right[0] || nativeReleaseCompare(left, right) < 0 || right[0] == 0 && left[1] != right[1] || targetOp == "~" && left[1] != right[1] {
		return false
	}
	if resolved {
		return op == ""
	}
	return op == targetOp || op == "" || targetOp == "^" && op == "~"
}

// NativeCoveragePaths returns the immutable evidence paths needed for coverage.
func NativeCoveragePaths(intents []map[string]any) map[string]bool {
	result := map[string]bool{}
	for _, item := range intents {
		filename, _ := item["manifest"].(string)
		result[filename] = true
		for _, lock := range nativeStringList(item["lock_manifests"]) {
			result[lock] = true
		}
		if item["ecosystem"] == "npm" && path.Base(filename) == "package.json" {
			for _, name := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
				result[path.Join(path.Dir(filename), name)] = true
			}
		}
		if item["ecosystem"] == "actions" {
			stem := nativeActionStem(filename)
			result[stem+".md"], result[stem+".lock.yml"], result[".github/aw/actions-lock.json"] = true, true, true
		}
	}
	return result
}

// Native string lists accept both decoded JSON and internally constructed lists.
func nativeStringList(value any) []string {
	if list, ok := value.([]string); ok {
		return list
	}
	result := []string{}
	if list, ok := value.([]any); ok {
		for _, value := range list {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

// Native action stems identify editable source and compiled workflow pairs.
func nativeActionStem(filename string) string {
	for _, suffix := range []string{".lock.yml", ".md", ".yml", ".yaml"} {
		if strings.HasSuffix(filename, suffix) {
			return strings.TrimSuffix(filename, suffix)
		}
	}
	return filename
}

// NativeVerifyCoverage proves every source intent against live consumer contents.
func NativeVerifyCoverage(ctx context.Context, read NativeReadFunc, intents []map[string]any, reader func(string) (string, error), available map[string]bool) error {
	if len(intents) == 0 {
		return nativeReject("missing coverage intent")
	}
	for _, item := range intents {
		if err := nativeValidateCoverageIntent(item); err != nil {
			return err
		}
		filename, _ := item["manifest"].(string)
		paths := []string{filename}
		if item["ecosystem"] == "actions" {
			if available != nil {
				stem := nativeActionStem(filename)
				if !available[filename] {
					paths = []string{stem + ".md", stem + ".lock.yml"}
					if !available[paths[0]] || !available[paths[1]] {
						return nativeReject("obsolete workflow has no live source and compiled replacement")
					}
				} else if strings.HasSuffix(filename, ".md") {
					if !available[stem+".lock.yml"] {
						return nativeReject("live action source lacks compiled consumer coverage")
					}
					paths = append(paths, stem+".lock.yml")
				} else if strings.HasSuffix(filename, ".lock.yml") && available[stem+".md"] {
					paths = append(paths, stem+".md")
				}
			}
			if err := nativeVerifyActionCoverage(ctx, read, item, reader, paths, available); err != nil {
				return err
			}
			continue
		}
		text, err := reader(filename)
		if err != nil {
			return err
		}
		versions, err := nativeManifestVersions(filename, text)
		if err != nil {
			return err
		}
		actual, exists := versions[nativeKey(item["key"])]
		var version any
		if exists {
			version = actual.Version
		}
		covered := nativeVersionCovers(version, item["target"])
		if item["ecosystem"] == "npm" {
			covered = nativeNPMCovers(version, item["target"], path.Base(filename) != "package.json")
		}
		if !covered {
			return nativeReject("upgrade intent is not covered: " + filename)
		}
		if item["ecosystem"] == "npm" && path.Base(filename) == "package.json" {
			locks := map[string]bool{}
			for _, lock := range nativeStringList(item["lock_manifests"]) {
				locks[lock] = true
			}
			for _, name := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
				lock := path.Join(path.Dir(filename), name)
				if available != nil && available[lock] {
					locks[lock] = true
				}
			}
			for lock := range locks {
				text, err := reader(lock)
				if err != nil {
					return err
				}
				resolutions, err := nativeReadLock(text)
				if err != nil {
					return err
				}
				resolution, ok := resolutions["node_modules/"+item["package"].(string)]
				if !ok || !nativeNPMCovers(resolution.Version, item["target"], true) {
					return nativeReject("npm primary family lacks compatible lock resolution: " + lock)
				}
			}
		}
	}
	return nil
}

// Native lock reading reuses the canonical resolved graph parser.
func nativeReadLock(text string) (map[string]nativeResolved, error) {
	value, err := nativeDecode([]byte(text))
	if err != nil {
		return nil, err
	}
	document, err := nativeObject(value)
	if err != nil {
		return nil, err
	}
	return nativeLockVersions(document)
}

// Native action coverage checks pins, compiler inventories and official tag commits.
func nativeVerifyActionCoverage(ctx context.Context, read NativeReadFunc, item map[string]any, reader func(string) (string, error), paths []string, available map[string]bool) error {
	pkg, target := item["package"].(string), item["target"].(string)
	entries := map[string]any{}
	for _, filename := range paths {
		if available != nil && available[".github/aw/actions-lock.json"] && (strings.HasSuffix(filename, ".md") || strings.HasSuffix(filename, ".lock.yml")) {
			text, err := reader(".github/aw/actions-lock.json")
			if err != nil {
				return err
			}
			value, err := nativeDecode([]byte(text))
			if err != nil {
				return err
			}
			document, err := nativeObject(value)
			if err != nil {
				return err
			}
			var ok bool
			entries, ok = document["entries"].(map[string]any)
			if !ok {
				return nativeReject("invalid compiler actions lock")
			}
			break
		}
	}
	references := map[string]bool{}
	for _, filename := range paths {
		text, err := reader(filename)
		if err != nil {
			return err
		}
		versions, err := nativeManifestVersions(filename, text)
		if err != nil {
			return err
		}
		pins := []string{}
		for _, version := range versions {
			if version.Package == pkg {
				pins = append(pins, version.Version)
			}
		}
		injected := strings.HasSuffix(item["manifest"].(string), ".lock.yml") && strings.HasSuffix(filename, ".md") && len(pins) == 0
		if len(pins) == 0 && !injected {
			return nativeReject("live Actions source/lock does not cover target: " + filename)
		}
		for _, pin := range pins {
			if pin == target {
				continue
			}
			entry, ok := entries[pkg+"@"+pin].(map[string]any)
			if !strings.HasSuffix(filename, ".md") || nativeSHA.MatchString(pin) || !ok || len(entry) != 3 || entry["repo"] != pkg || entry["version"] != pin || entry["sha"] != target {
				return nativeReject("live Actions source tag lacks intended compiler lock resolution: " + filename)
			}
			if err := NativeVerifyOfficialRelease(ctx, read, nativeUpgradeFamily("actions", pkg), pin, target); err != nil {
				return err
			}
			references[pin] = true
		}
		if strings.HasSuffix(filename, ".lock.yml") {
			headers := 0
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "# gh-aw-manifest: ") {
					headers++
					if headers > 1 {
						return nativeReject("ambiguous compiler action inventory")
					}
					value, err := nativeDecode([]byte(strings.TrimPrefix(line, "# gh-aw-manifest: ")))
					if err != nil {
						return err
					}
					document, err := nativeObject(value)
					if err != nil {
						return err
					}
					actions, ok := document["actions"].([]any)
					if !ok {
						return nativeReject("invalid compiler action inventory")
					}
					found := false
					for _, raw := range actions {
						entry, ok := raw.(map[string]any)
						if !ok {
							return nativeReject("invalid compiler action inventory")
						}
						if entry["repo"] == pkg {
							found = true
							version, ok := entry["version"].(string)
							if !ok || version == "" || len(entry) != 3 || entry["sha"] != target {
								return nativeReject("compiler action inventory does not cover target")
							}
							references[version] = true
						}
					}
					if !found {
						return nativeReject("compiler action inventory does not cover target")
					}
				}
			}
		}
	}
	for key, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !strings.HasPrefix(key, pkg+"@") && (!ok || entry["repo"] != pkg) {
			continue
		}
		version, vok := entry["version"].(string)
		if !ok || !vok || len(entry) != 3 || entry["repo"] != pkg || key != pkg+"@"+version {
			return nativeReject("malformed relevant compiler action lock entry")
		}
		if (len(references) == 0 || references[version]) && entry["sha"] != target {
			return nativeReject("compiler actions lock does not cover target: " + key)
		}
	}
	return nil
}
