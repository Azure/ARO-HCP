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

package configoverride

import (
	"fmt"
	"regexp"
	"strings"
)

// RawConfigOverridesEnv carries raw configuration overrides, one
// "path=value" entry per line. The openshift/release provision step declares
// it so a job (or an unmerged rehearsal PR) can tune any configuration key
// without a dedicated change in this repository.
const RawConfigOverridesEnv = "RAW_CONFIG_OVERRIDES"

// pathSegment matches a configuration map key. Configuration keys never
// contain dots, so "." is an unambiguous path separator.
var pathSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// RawOverride sets one key of the generated override document. Path is rooted
// at the document root, not at an overlay, so the author chooses the overlay
// that must win: a region entry in config/config.yaml beats a "defaults"
// override, hence the region must be targeted explicitly in that case.
type RawOverride struct {
	// Entry is the original "path=value" text, kept for logging.
	Entry string
	Path  []string
	Value interface{}
}

// ParseRawOverrides parses "path=value" entries. The path is split from the
// value at the first "=", so values may contain "=" (for example AKS tags).
// The value is a YAML scalar or flow collection, so 3 is an integer, true a
// boolean and "3" a string. Blank entries are skipped, which lets a
// multi-line environment variable be split on newlines.
func ParseRawOverrides(entries []string) ([]RawOverride, error) {
	var overrides []RawOverride
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		override, err := parseRawOverride(entry)
		if err != nil {
			return nil, err
		}
		overrides = append(overrides, override)
	}
	return overrides, nil
}

func parseRawOverride(entry string) (RawOverride, error) {
	path, value, ok := strings.Cut(entry, "=")
	if !ok {
		return RawOverride{}, fmt.Errorf("raw config override %q must have the form path.to.key=value", entry)
	}
	path = strings.TrimSpace(path)
	segments := strings.Split(path, ".")
	for _, segment := range segments {
		if !pathSegment.MatchString(segment) {
			return RawOverride{}, fmt.Errorf("raw config override %q: path %q must be dot-separated keys of letters, digits, '_' or '-'", entry, path)
		}
	}
	if strings.TrimSpace(value) == "" {
		return RawOverride{}, fmt.Errorf("raw config override %q: value is empty; use \"\" for an empty string or null to clear the key", entry)
	}
	var parsed interface{}
	if err := decodeYAML([]byte(value), &parsed); err != nil {
		return RawOverride{}, fmt.Errorf("raw config override %q: value is not valid YAML (quote values containing ': ' or ' #'): %w", entry, err)
	}
	return RawOverride{Entry: entry, Path: segments, Value: parsed}, nil
}

// checkRawOverrideConflicts rejects entries that set the same key or where
// one entry sets a key inside another's value: the outcome would depend on
// entry order.
func checkRawOverrideConflicts(overrides []RawOverride) error {
	for i, a := range overrides {
		for _, b := range overrides[:i] {
			shared := min(len(a.Path), len(b.Path))
			if strings.Join(a.Path[:shared], ".") == strings.Join(b.Path[:shared], ".") {
				return fmt.Errorf("raw config overrides %q and %q overlap", b.Entry, a.Entry)
			}
		}
	}
	return nil
}

// applyRawOverrides sets each override into root, replacing whatever the
// generator or the Hypershift overlay produced at that exact key.
func applyRawOverrides(root map[string]interface{}, overrides []RawOverride) error {
	for _, override := range overrides {
		current := root
		for i, key := range override.Path[:len(override.Path)-1] {
			next, ok := current[key].(map[string]interface{})
			if !ok {
				if existing, present := current[key]; present && existing != nil {
					return fmt.Errorf("raw config override %q: %q is not a mapping and cannot contain keys", override.Entry, strings.Join(override.Path[:i+1], "."))
				}
				next = map[string]interface{}{}
				current[key] = next
			}
			current = next
		}
		current[override.Path[len(override.Path)-1]] = override.Value
	}
	return nil
}
