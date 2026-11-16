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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var npmSections = map[string]bool{
	"dependencies": true, "devDependencies": true, "peerDependencies": true,
	"optionalDependencies": true, "overrides": true,
}

var npmToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|[{}\[\]:,]|-?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null`)

// pathPart is one dependency-object path segment; nil marks an unknown root.
type pathPart *string

// NpmDependencies extracts changed npm package names from diff hunks.
func NpmDependencies(hunks []Hunk) (map[string]bool, error) {
	entries := newSideEntries()
	for _, hunk := range hunks {
		for _, side := range []string{"-", "+"} {
			start := hunk.OldStart
			if side == "+" {
				start = hunk.NewStart
			}
			stack := [][]pathPart{}
			if start > 1 {
				stack = append(stack, []pathPart{nil})
			}
			var key *string
			sideLines := [][2]string{}
			for _, pair := range hunk.Lines {
				if pair[0] == " " || pair[0] == side {
					sideLines = append(sideLines, pair)
				}
			}
			var text strings.Builder
			for _, pair := range sideLines {
				text.WriteString(pair[1])
				text.WriteString("\n")
			}
			body := strings.TrimSuffix(text.String(), "\n")
			if body == "" {
				continue
			}
			if start <= 1 && strings.HasPrefix(strings.TrimSpace(body), "{") {
				if err := validateJSONDocument(body); err != nil {
					return nil, fmt.Errorf("cannot parse npm manifest JSON: %w", err)
				}
			}
			for _, pair := range sideLines {
				marker, line := pair[0], pair[1]
				matches := npmToken.FindAllStringIndex(line, -1)
				position := 0
				for index, span := range matches {
					if strings.TrimSpace(line[position:span[0]]) != "" {
						return nil, errors.New("cannot tokenize npm manifest change")
					}
					position = span[1]
					value := line[span[0]:span[1]]
					current := []pathPart{}
					if len(stack) > 0 {
						current = stack[len(stack)-1]
					}
					isKey := strings.HasPrefix(value, `"`) && index+1 < len(matches) &&
						line[matches[index+1][0]:matches[index+1][1]] == ":"
					if isKey {
						decoded, err := strconv.Unquote(value)
						if err != nil {
							return nil, errors.New("cannot tokenize npm manifest change")
						}
						key = &decoded
					} else if value == "{" || value == "[" {
						next := append([]pathPart{}, current...)
						if key != nil {
							next = append(next, key)
						}
						stack = append(stack, next)
						key = nil
					} else if value == "}" || value == "]" {
						if len(stack) > 0 && !(len(stack[len(stack)-1]) == 1 && stack[len(stack)-1][0] == nil) {
							stack = stack[:len(stack)-1]
						}
						key = nil
					} else if value == "," {
						key = nil
					}
					switch value {
					case "{", "}", "[", "]", ":", ",":
						continue
					}
					dependencyPath := append([]pathPart{}, current...)
					if key != nil {
						dependencyPath = append(dependencyPath, key)
					}
					section := -1
					for i, part := range dependencyPath {
						if part != nil && npmSections[*part] {
							section = i
							break
						}
					}
					if section >= 0 {
						names := dependencyPath[section+1:]
						if len(names) > 0 {
							if *dependencyPath[section] != "overrides" {
								names = names[:1]
							}
							if !isKey {
								packages := map[string]bool{}
								for _, name := range names {
									if name != nil {
										packages[stripOverrideScope(*name)] = true
									}
								}
								canonical, err := canonicalJSON(value)
								if err != nil {
									return nil, errors.New("cannot tokenize npm manifest change")
								}
								pathKey := ""
								for _, part := range dependencyPath[section:] {
									if part == nil {
										pathKey += "\x00"
									} else {
										pathKey += *part + "\x00"
									}
								}
								keyText := pathKey + "\x01" + canonical
								for pkg := range packages {
									entries.set(side, keyText, pkg)
								}
							}
						} else if !isKey && marker == side {
							return nil, errors.New("cannot classify npm dependency entry")
						}
					} else if marker == side && len(dependencyPath) > 0 && dependencyPath[0] == nil &&
						len(dependencyPath) < 3 &&
						!(isKey && index+2 < len(matches) &&
							(line[matches[index+2][0]:matches[index+2][1]] == "{" ||
								line[matches[index+2][0]:matches[index+2][1]] == "[")) {
						return nil, errors.New("npm dependency section is outside diff context")
					}
				}
				if strings.TrimSpace(line[position:]) != "" {
					return nil, errors.New("cannot tokenize npm manifest change")
				}
			}
		}
	}
	return entries.changedPackages(), nil
}

// validateJSONDocument reports whether the document parses as JSON.
func validateJSONDocument(document string) error {
	var value any
	return json.Unmarshal([]byte(document), &value)
}

// canonicalJSON normalizes a scalar token the way the Python json round-trip did.
func canonicalJSON(value string) (string, error) {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// stripOverrideScope removes an override's @scope suffix, keeping a leading @.
func stripOverrideScope(name string) string {
	if index := strings.Index(name[1:], "@"); index >= 0 {
		return name[:index+1]
	}
	return name
}

// LockEntry is one resolved npm package identity.
type LockEntry struct {
	Package   string
	Version   string
	Resolved  string
	Integrity string
}

// LockVersions reads resolved identities from npm lockfile versions 1 to 3.
func LockVersions(document map[string]any) (map[string]LockEntry, error) {
	version, ok := document["lockfileVersion"].(float64)
	if !ok || (version != 1 && version != 2 && version != 3) {
		return nil, errors.New("unsupported npm lockfile format")
	}
	result := map[string]LockEntry{}
	if version >= 2 {
		packages, ok := document["packages"].(map[string]any)
		if !ok {
			return nil, errors.New("npm lockfile packages are unavailable")
		}
		for packagePath, raw := range packages {
			if packagePath == "" {
				continue
			}
			entry, ok := raw.(map[string]any)
			if !ok || !strings.Contains(packagePath, "node_modules/") {
				return nil, errors.New("cannot identify npm lockfile package")
			}
			packageName := packagePath[strings.LastIndex(packagePath, "node_modules/")+len("node_modules/"):]
			if linked, _ := entry["link"].(bool); linked {
				return nil, errors.New("npm workspace link requires conservative deferral")
			}
			resolvedVersion, ok := entry["version"].(string)
			if !ok {
				return nil, errors.New("npm resolved version is unavailable")
			}
			result[packagePath] = LockEntry{
				Package: packageName, Version: resolvedVersion,
				Resolved: stringValue(entry["resolved"]), Integrity: stringValue(entry["integrity"]),
			}
		}
		return result, nil
	}
	var visit func(dependencies map[string]any, parent string) error
	visit = func(dependencies map[string]any, parent string) error {
		for packageName, raw := range dependencies {
			entry, ok := raw.(map[string]any)
			if !ok {
				return errors.New("invalid npm lockfile dependencies")
			}
			resolvedVersion, ok := entry["version"].(string)
			if !ok {
				return errors.New("npm resolved version is unavailable")
			}
			packagePath := parent + "node_modules/" + packageName
			result[packagePath] = LockEntry{
				Package: packageName, Version: resolvedVersion,
				Resolved: stringValue(entry["resolved"]), Integrity: stringValue(entry["integrity"]),
			}
			if children, ok := entry["dependencies"].(map[string]any); ok {
				if err := visit(children, packagePath+"/"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	dependencies, _ := document["dependencies"].(map[string]any)
	if dependencies == nil {
		dependencies = map[string]any{}
	}
	if err := visit(dependencies, ""); err != nil {
		return nil, err
	}
	return result, nil
}

// stringValue renders an optional JSON string field.
func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
