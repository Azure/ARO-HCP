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
	"regexp"
	"strconv"
	"strings"
)

var nativeOverrideSelector = regexp.MustCompile(`^(@[^/]+/[^@]+|[^@]+)(?:@(.+))?$`)
var nativeOverrideConstraint = regexp.MustCompile(`^(\^|~|>=)?([0-9]+)(?:\.([0-9]+|x|\*))?(?:\.([0-9]+|x|\*))?(?:-[A-Za-z0-9_.-]+)?$`)

// Native row conversion accepts internal and decoded source intent arrays.
func nativeRowsValue(value any) ([]map[string]any, error) {
	if rows, ok := value.([]map[string]any); ok {
		return rows, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, nativeReject("missing source intents")
	}
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		row, err := nativeObject(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Native npm regeneration cannot change package identity or downgrade shared paths.
func nativePreserveNPMResolutions(old, current map[string]nativeResolved) error {
	for filename, before := range old {
		after, ok := current[filename]
		if !ok {
			continue
		}
		if before.Package != after.Package {
			return nativeReject("npm lock path changed package identity")
		}
		if before.Version == after.Version {
			continue
		}
		_, left, lok := nativeNPMVersion(before.Version)
		_, right, rok := nativeNPMVersion(after.Version)
		if !lok || !rok || nativeReleaseCompare(right, left) < 0 {
			return nativeReject("npm regeneration downgrades an existing resolution or has unverifiable versions")
		}
	}
	return nil
}

// Native override matching supports only the canonical bounded selector grammar.
func nativeOverrideMatches(selector, version string) (string, bool, error) {
	match := nativeOverrideSelector.FindStringSubmatch(selector)
	if match == nil {
		return "", false, nativeReject("invalid npm override selector")
	}
	if match[2] == "" {
		return match[1], true, nil
	}
	op, actual, ok := nativeNPMVersion(version)
	if !ok || op != "" {
		return "", false, nativeReject("unverifiable overridden npm version")
	}
	constraint := nativeOverrideConstraint.FindStringSubmatch(match[2])
	if constraint == nil {
		return "", false, nativeReject("unsupported npm override selector range")
	}
	var floor [3]int
	for i := range floor {
		part := constraint[i+2]
		if part != "" && part != "x" && part != "*" {
			number, err := strconv.Atoi(part)
			if err != nil {
				return "", false, err
			}
			floor[i] = number
		}
	}
	selected := false
	switch constraint[1] {
	case ">=":
		selected = nativeReleaseCompare(actual, floor) >= 0
	case "^":
		selected = nativeReleaseCompare(actual, floor) >= 0 && actual[0] == floor[0] && (floor[0] != 0 || actual[1] == floor[1])
	case "~":
		selected = nativeReleaseCompare(actual, floor) >= 0 && actual[0] == floor[0] && actual[1] == floor[1]
	default:
		selected = actual[0] == floor[0] && (constraint[3] == "" || constraint[3] == "x" || constraint[3] == "*" || actual[1] == floor[1]) && (constraint[4] == "" || constraint[4] == "x" || constraint[4] == "*" || actual[2] == floor[2])
	}
	return match[1], selected, nil
}

// Native npm overrides apply preserved floors only within selected parent scopes.
func nativeValidateNPMOverrides(text string, resolved map[string]nativeResolved) error {
	manifest, err := nativeManifestVersions("package.json", text)
	if err != nil {
		return err
	}
	child := func(parent, pkg string) string {
		for scope := parent; scope != ""; {
			candidate := scope + "/node_modules/" + pkg
			if _, ok := resolved[candidate]; ok {
				return candidate
			}
			index := strings.LastIndex(scope, "/node_modules/")
			if index < 0 {
				scope = ""
			} else {
				scope = scope[:index]
			}
		}
		candidate := "node_modules/" + pkg
		if _, ok := resolved[candidate]; ok {
			return candidate
		}
		return ""
	}
	for _, item := range manifest {
		key := nativeStringList(item.Key)
		if len(key) == 0 || key[0] != "overrides" {
			continue
		}
		floor := item.Version
		if strings.HasPrefix(floor, "$") {
			reference := strings.TrimPrefix(floor, "$")
			choices := map[string]bool{}
			for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
				if version, ok := manifest[nativeKey([]string{section, reference})]; ok {
					choices[version.Version] = true
				}
			}
			if len(choices) != 1 {
				return nativeReject("unresolved npm override dependency reference")
			}
			for version := range choices {
				floor = version
			}
		}
		contexts := map[string]bool{"": true}
		for _, selector := range key[1:] {
			selected := map[string]bool{}
			for parent := range contexts {
				if selector == "." {
					if parent == "" {
						return nativeReject("root self override has no parent package")
					}
					selected[parent] = true
					continue
				}
				match := nativeOverrideSelector.FindStringSubmatch(selector)
				if match == nil {
					return nativeReject("invalid npm override selector")
				}
				pkg := match[1]
				candidates := []string{}
				if parent == "" {
					for filename, entry := range resolved {
						if entry.Package == pkg {
							candidates = append(candidates, filename)
						}
					}
				} else if filename := child(parent, pkg); filename != "" {
					candidates = append(candidates, filename)
				}
				for _, filename := range candidates {
					_, matches, err := nativeOverrideMatches(selector, resolved[filename].Version)
					if err != nil {
						return err
					}
					if matches {
						selected[filename] = true
					}
				}
			}
			contexts = selected
		}
		for filename := range contexts {
			actual := resolved[filename].Version
			satisfies := nativeNPMCovers(actual, floor, true)
			if len(key) <= 2 {
				op, left, lok := nativeNPMVersion(actual)
				_, right, rok := nativeNPMVersion(floor)
				satisfies = lok && rok && op == "" && nativeReleaseCompare(left, right) >= 0
			}
			if !satisfies {
				return nativeReject("npm lock violates preserved security override scope: " + strings.Join(key[1:], "/"))
			}
		}
	}
	return nil
}
