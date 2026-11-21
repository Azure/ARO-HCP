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
	"net/url"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Native versions retain the structural occurrence key and package identity.
type nativeVersion struct {
	Key     any
	Package string
	Version string
	Direct  bool
}

type nativeVersionSet map[string]nativeVersion

// Native keys encode structural identities unambiguously.
func nativeKey(key any) string {
	encoded, _ := json.Marshal(key)
	return string(encoded)
}

// Native ecosystems recognize only supported manifests.
func nativeEcosystem(filename string) string {
	switch path.Base(filename) {
	case "go.mod":
		return "go"
	case "package.json", "package-lock.json", "npm-shrinkwrap.json":
		return "npm"
	}
	if strings.HasPrefix(filename, ".github/workflows/") &&
		(strings.HasSuffix(filename, ".md") || strings.HasSuffix(filename, ".yml") || strings.HasSuffix(filename, ".yaml")) {
		return "actions"
	}
	return ""
}

// Native lock paths identify npm resolved graph manifests.
func nativeLockPath(filename string) bool {
	return path.Base(filename) == "package-lock.json" || path.Base(filename) == "npm-shrinkwrap.json"
}

var nativeGoEntry = regexp.MustCompile(`^(?:require\s+)?([A-Za-z0-9._~+/-]+)\s+(v[0-9][A-Za-z0-9.+_-]*)$`)
var nativeGoBlock = regexp.MustCompile(`^(require|replace|exclude|retract|godebug)\s*\($`)
var nativeActionLine = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*['"]?)([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)@([A-Za-z0-9_.-]+)(['"]?\s*(?:#.*)?)$`)
var nativeGoRequireBlock = regexp.MustCompile(`^require\s*\($`)
var nativeGoMaskedEntry = regexp.MustCompile(`^(?:require\s+)?[A-Za-z0-9._~+/-]+\s+v[0-9][A-Za-z0-9.+_-]*(?:\s*//.*)?$`)
var nativeCompilerHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Native lines match Python splitlines for ordinary repository text.
func nativeLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}

// Native manifest versions preserve dependency occurrence keys.
func nativeManifestVersions(filename, text string) (nativeVersionSet, error) {
	switch nativeEcosystem(filename) {
	case "go":
		return nativeGoVersions(text)
	case "actions":
		result := nativeVersionSet{}
		for index, line := range nativeLines(text) {
			match := nativeActionLine.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			key := []any{index, match[2]}
			result[nativeKey(key)] = nativeVersion{
				Key:     key,
				Package: match[2],
				Version: match[3],
				Direct:  true,
			}
		}
		return result, nil
	case "npm":
		value, err := nativeDecode([]byte(text))
		if err != nil {
			return nil, err
		}
		document, err := nativeObject(value)
		if err != nil {
			return nil, err
		}
		if nativeLockPath(filename) {
			locks, err := nativeLockVersions(document)
			if err != nil {
				return nil, err
			}
			result := nativeVersionSet{}
			for key, row := range locks {
				result[nativeKey(key)] = nativeVersion{
					Key:     key,
					Package: row.Package,
					Version: row.Version,
					Direct:  true,
				}
			}
			return result, nil
		}
		return nativeNPMVersions(document)
	default:
		return nativeVersionSet{}, nil
	}
}

// Native Go versions include requirements but no replace or toolchain directives.
func nativeGoVersions(text string) (nativeVersionSet, error) {
	result := nativeVersionSet{}
	block := ""
	for _, raw := range nativeLines(text) {
		line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
		if match := nativeGoBlock.FindStringSubmatch(line); match != nil {
			block = match[1]
			continue
		}
		if line == ")" {
			block = ""
			continue
		}
		match := nativeGoEntry.FindStringSubmatch(line)
		if match == nil || block != "require" && !strings.HasPrefix(line, "require ") {
			continue
		}
		key := nativeKey(match[1])
		if _, exists := result[key]; exists {
			return nil, nativeReject("duplicate Go requirement")
		}
		result[key] = nativeVersion{
			Key:     match[1],
			Package: match[1],
			Version: match[2],
			Direct:  nativeGoEntry.MatchString(strings.TrimSpace(raw)),
		}
	}
	return result, nil
}

// Native npm versions retain nested overrides and each dependency section.
func nativeNPMVersions(document map[string]any) (nativeVersionSet, error) {
	result := nativeVersionSet{}
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "overrides"} {
		entries := map[string]any{}
		if value, exists := document[section]; exists {
			var ok bool
			entries, ok = value.(map[string]any)
			if !ok {
				return nil, nativeReject("unsupported npm dependency section")
			}
		}
		if err := nativeNPMSection(result, entries, []string{section}, section == "overrides"); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Native npm sections recurse only through overrides, rejecting non-string versions.
func nativeNPMSection(result nativeVersionSet, entries map[string]any, prefix []string, overrides bool) error {
	for pkg, value := range entries {
		key := make([]string, len(prefix)+1)
		copy(key, prefix)
		key[len(prefix)] = pkg
		if children, ok := value.(map[string]any); ok && overrides {
			if err := nativeNPMSection(result, children, key, true); err != nil {
				return err
			}
			continue
		}
		version, ok := value.(string)
		if !ok {
			return nativeReject("unsupported npm dependency version")
		}
		name := pkg
		if overrides && name == "." {
			if len(prefix) < 2 {
				return nativeReject("root self override has no parent package")
			}
			name = prefix[len(prefix)-1]
		}
		if len(name) > 1 {
			if at := strings.Index(name[1:], "@"); at >= 0 {
				name = name[:at+1]
			}
		}
		result[nativeKey(key)] = nativeVersion{
			Key:     key,
			Package: name,
			Version: version,
			Direct:  true,
		}
	}
	return nil
}

// Native resolutions retain complete npm lock package evidence.
type nativeResolved struct {
	Package   string
	Version   string
	Resolved  any
	Integrity any
}

// Native lock versions support npm formats one through three, rejecting workspace links.
func nativeLockVersions(document map[string]any) (map[string]nativeResolved, error) {
	version, ok := nativeInteger(document["lockfileVersion"])
	if !ok || version < 1 || version > 3 {
		return nil, nativeReject("unsupported npm lockfile format")
	}
	result := map[string]nativeResolved{}
	if version >= 2 {
		packages, ok := document["packages"].(map[string]any)
		if !ok {
			return nil, nativeReject("npm lockfile packages are unavailable")
		}
		for filename, value := range packages {
			if filename == "" {
				continue
			}
			entry, ok := value.(map[string]any)
			index := strings.LastIndex(filename, "node_modules/")
			if !ok || index < 0 {
				return nil, nativeReject("cannot identify npm lockfile package")
			}
			resolved, ok := entry["version"].(string)
			if !ok || entry["link"] == true {
				return nil, nativeReject("npm resolved version is unavailable or workspace link")
			}
			result[filename] = nativeResolved{
				Package:   filename[index+len("node_modules/"):],
				Version:   resolved,
				Resolved:  entry["resolved"],
				Integrity: entry["integrity"],
			}
		}
		return result, nil
	}
	dependencies, exists := document["dependencies"]
	if !exists {
		dependencies = map[string]any{}
	}
	if err := nativeLockDependencies(result, dependencies, ""); err != nil {
		return nil, err
	}
	return result, nil
}

// Native v1 lock dependencies retain nested installation paths.
func nativeLockDependencies(result map[string]nativeResolved, value any, parent string) error {
	entries, ok := value.(map[string]any)
	if !ok {
		return nativeReject("invalid npm lockfile dependencies")
	}
	for pkg, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return nativeReject("npm resolved version is unavailable")
		}
		version, ok := entry["version"].(string)
		if !ok {
			return nativeReject("npm resolved version is unavailable")
		}
		filename := parent + "node_modules/" + pkg
		result[filename] = nativeResolved{
			Package:   pkg,
			Version:   version,
			Resolved:  entry["resolved"],
			Integrity: entry["integrity"],
		}
		children, exists := entry["dependencies"]
		if !exists {
			children = map[string]any{}
		}
		if err := nativeLockDependencies(result, children, filename+"/"); err != nil {
			return err
		}
	}
	return nil
}

// Native Go masks remove only solver-owned requirements, retaining other directives.
func nativeGoMask(text string) ([]string, error) {
	result := []string{}
	inRequire := false
	for _, line := range nativeLines(text) {
		value := strings.TrimSpace(line)
		switch {
		case nativeGoRequireBlock.MatchString(value):
			if inRequire {
				return nil, nativeReject("nested Go require block")
			}
			inRequire = true
		case inRequire && value == ")":
			inRequire = false
		case inRequire:
			if value != "" && !strings.HasPrefix(value, "//") && !nativeGoMaskedEntry.MatchString(value) {
				return nil, nativeReject("invalid Go require entry")
			}
		case value != "" && !nativeGoMaskedEntry.MatchString(value):
			result = append(result, value)
		}
	}
	if inRequire {
		return nil, nativeReject("unterminated Go require block")
	}
	return result, nil
}

// Native npm masks replace changed leaves, retaining all non-dependency fields.
func nativeMaskNPM(text string, changed []nativeVersion) (any, error) {
	value, err := nativeDecode([]byte(text))
	if err != nil {
		return nil, err
	}
	document, err := nativeObject(value)
	if err != nil {
		return nil, err
	}
	for _, item := range changed {
		key := item.Key.([]string)
		node := document
		for _, part := range key[:len(key)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				return nil, nativeReject("missing npm dependency path")
			}
			node = child
		}
		node[key[len(key)-1]] = "<VERSION>"
	}
	return document, nil
}

// Native changes validate manifest-only edits and record dependency transitions.
func nativeChanges(ctx context.Context, read NativeReadFunc, filename, before, after string) ([]map[string]any, error) {
	old, err := nativeManifestVersions(filename, before)
	if err != nil {
		return nil, err
	}
	current, err := nativeManifestVersions(filename, after)
	if err != nil {
		return nil, err
	}
	ecosystem := nativeEcosystem(filename)
	keys := map[string]bool{}
	for key := range old {
		keys[key] = true
	}
	for key := range current {
		keys[key] = true
	}
	ordered := []string{}
	for key := range keys {
		left, leftOK := old[key]
		right, rightOK := current[key]
		if leftOK != rightOK || left.Version != right.Version {
			ordered = append(ordered, key)
		}
	}
	sort.Strings(ordered)
	changed := make([]nativeVersion, 0, len(ordered))
	result := make([]map[string]any, 0, len(ordered))
	for _, key := range ordered {
		left, leftOK := old[key]
		right, rightOK := current[key]
		if ecosystem != "go" && !nativeLockPath(filename) && (!leftOK || !rightOK) {
			return nil, nativeReject("added/removed dependency is not a version upgrade")
		}
		item := right
		if !rightOK {
			item = left
		}
		changed = append(changed, item)
		var oldVersion, newVersion any
		if leftOK {
			oldVersion = left.Version
		}
		if rightOK {
			newVersion = right.Version
		}
		result = append(result, map[string]any{
			"ecosystem": ecosystem,
			"package":   item.Package,
			"old":       oldVersion,
			"target":    newVersion,
			"manifest":  filename,
			"direct":    item.Direct,
			"key":       item.Key,
		})
	}
	switch {
	case ecosystem == "go":
		left, err := nativeGoMask(before)
		if err != nil {
			return nil, err
		}
		right, err := nativeGoMask(after)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(left, right) {
			return nil, nativeReject("non-version Go manifest change")
		}
	case path.Base(filename) == "package.json":
		left, err := nativeMaskNPM(before, changed)
		if err != nil {
			return nil, err
		}
		right, err := nativeMaskNPM(after, changed)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(left, right) {
			return nil, nativeReject("non-dependency npm manifest change")
		}
	case ecosystem == "actions":
		if err := nativeActionChanges(ctx, read, filename, before, after, ordered, old, current); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Native action changes permit immutable pins and descriptive version comments only.
func nativeActionChanges(ctx context.Context, read NativeReadFunc, filename, before, after string, ordered []string, old, current nativeVersionSet) error {
	left, right := nativeLines(before), nativeLines(after)
	if len(left) != len(right) {
		return nativeReject("action source layout changed")
	}
	for _, key := range ordered {
		previous, next := old[key], current[key]
		if !nativeSHA.MatchString(next.Version) {
			return nativeReject("Actions targets must be full immutable SHAs")
		}
		index := next.Key.([]any)[0].(int)
		left[index] = strings.Replace(left[index], "@"+previous.Version, "@<PIN>", 1)
		right[index] = strings.Replace(right[index], "@"+next.Version, "@<PIN>", 1)
		left[index] = strings.TrimRight(strings.SplitN(left[index], "#", 2)[0], " \t")
		right[index] = strings.TrimRight(strings.SplitN(right[index], "#", 2)[0], " \t")
	}
	if strings.HasSuffix(filename, ".lock.yml") {
		if err := nativeCompilerHeaders(ctx, read, left, right, current); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(left, right) {
		return nativeReject("non-pin Actions source change")
	}
	return nil
}

// Native compiler headers permit only hashes and official intended action descriptions.
func nativeCompilerHeaders(ctx context.Context, read NativeReadFunc, leftLines, rightLines []string, pins nativeVersionSet) error {
	for _, prefix := range []string{"# gh-aw-metadata: ", "# gh-aw-manifest: "} {
		leftIndexes, rightIndexes := []int{}, []int{}
		for index := range leftLines {
			if strings.HasPrefix(leftLines[index], prefix) {
				leftIndexes = append(leftIndexes, index)
			}
			if strings.HasPrefix(rightLines[index], prefix) {
				rightIndexes = append(rightIndexes, index)
			}
		}
		if len(leftIndexes) > 1 || !reflect.DeepEqual(leftIndexes, rightIndexes) {
			return nativeReject("compiler header layout changed")
		}
		for _, index := range leftIndexes {
			leftValue, err := nativeDecode([]byte(strings.TrimPrefix(leftLines[index], prefix)))
			if err != nil {
				return err
			}
			rightValue, err := nativeDecode([]byte(strings.TrimPrefix(rightLines[index], prefix)))
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
			if prefix == "# gh-aw-metadata: " {
				for _, key := range []string{"frontmatter_hash", "body_hash"} {
					for _, document := range []map[string]any{left, right} {
						hash, ok := document[key].(string)
						if !ok || !nativeCompilerHash.MatchString(hash) {
							return nativeReject("invalid compiler source hash")
						}
						document[key] = "<HASH>"
					}
				}
			} else if err := nativeCompilerActions(ctx, read, left, right, pins); err != nil {
				return err
			}
			if !reflect.DeepEqual(left, right) {
				return nativeReject("compiler metadata changes executable provenance or secrets")
			}
			leftLines[index] = prefix + "<VALIDATED>"
			rightLines[index] = leftLines[index]
		}
	}
	return nil
}

// Native compiler action inventories may describe only the intended official pins.
func nativeCompilerActions(ctx context.Context, read NativeReadFunc, left, right map[string]any, pins nativeVersionSet) error {
	previous, previousOK := left["actions"].([]any)
	current, currentOK := right["actions"].([]any)
	if !previousOK || !currentOK || len(previous) != len(current) {
		return nativeReject("compiler action inventory changed")
	}
	for index := range previous {
		before, err := nativeObject(previous[index])
		if err != nil {
			return err
		}
		after, err := nativeObject(current[index])
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before["repo"], after["repo"]) {
			return nativeReject("compiler action identity changed")
		}
		if reflect.DeepEqual(before, after) {
			continue
		}
		repository, repoOK := after["repo"].(string)
		sha, shaOK := after["sha"].(string)
		version, versionOK := after["version"].(string)
		if !repoOK || !shaOK || !versionOK || len(before) != 3 || len(after) != 3 ||
			before["sha"] == nil || before["version"] == nil {
			return nativeReject("unrelated compiler manifest pin")
		}
		allowed := false
		for _, pin := range pins {
			allowed = allowed || pin.Package == repository && pin.Version == sha
		}
		if !allowed {
			return nativeReject("unrelated compiler manifest pin")
		}
		before["sha"] = sha
		if !reflect.DeepEqual(before["version"], after["version"]) {
			endpoint := "repos/" + nativeUpgradeFamily("actions", repository) + "/commits/" + url.PathEscape(version)
			value, err := nativeRead(ctx, read, endpoint, false, false)
			if err != nil {
				return err
			}
			commit, err := nativeObject(value)
			if err != nil {
				return err
			}
			if commit["sha"] != sha {
				return nativeReject("compiler manifest release does not resolve to intended pin")
			}
			before["version"] = version
		}
	}
	return nil
}
