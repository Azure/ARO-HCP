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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// MaxNativeInventoryBytes bounds untrusted inventory input before decoding.
const MaxNativeInventoryBytes = 16 * 1024 * 1024

var (
	nativeGroupPattern  = regexp.MustCompile(`^[0-9a-f]{24}$`)
	nativeNumberPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// NativeSelection preserves the selected entry and its sorted unique consumers.
type NativeSelection struct {
	BaseSHA   string          `json:"base_sha"`
	Group     json.RawMessage `json:"group"`
	Manifests []string        `json:"manifests"`
}

type nativeInventoryGroup struct {
	Group      string `json:"group"`
	Target     string `json:"target"`
	Ecosystem  string `json:"ecosystem"`
	Family     string `json:"family"`
	NextAction struct {
		Action string `json:"action"`
	} `json:"next_action"`
	Blockers []json.RawMessage `json:"blockers"`
	Sources  []struct {
		Number    json.RawMessage `json:"number"`
		HeadSHA   string          `json:"head_sha"`
		Group     string          `json:"group"`
		Target    string          `json:"target"`
		Ecosystem string          `json:"ecosystem"`
		Family    string          `json:"family"`
		Intents   []struct {
			Ecosystem     string          `json:"ecosystem"`
			Package       string          `json:"package"`
			Manifest      string          `json:"manifest"`
			LockManifests json.RawMessage `json:"lock_manifests"`
		} `json:"intents"`
	} `json:"sources"`
}

// SelectNativeInventoryGroup selects exactly one eligible family/target offline.
// Unknown selected-entry metadata is retained without converting JSON numbers.
func SelectNativeInventoryGroup(input io.Reader, repository, groupID, target string) (NativeSelection, error) {
	var result NativeSelection
	if !nativeGroupPattern.MatchString(groupID) || target == "" {
		return result, errors.New("invalid inventory group or target")
	}
	content, err := io.ReadAll(io.LimitReader(input, MaxNativeInventoryBytes+1))
	if err != nil {
		return result, fmt.Errorf("read native inventory: %w", err)
	}
	if len(content) > MaxNativeInventoryBytes {
		return result, errors.New("native inventory exceeds 16 MiB")
	}
	if !utf8.Valid(content) {
		return result, errors.New("native inventory is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := validateNativeJSON(decoder, 0); err != nil {
		return result, fmt.Errorf("invalid native inventory JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, errors.New("native inventory has trailing JSON")
	}
	var inventory struct {
		Repository string            `json:"repository"`
		BaseSHA    string            `json:"base_sha"`
		Groups     []json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return result, fmt.Errorf("invalid native inventory: %w", err)
	}
	if err := policy.ValidateMaintenanceTarget(repository, 1, inventory.BaseSHA); err != nil {
		return result, err
	}
	if inventory.Repository != repository || inventory.Groups == nil {
		return result, errors.New("inventory repository or groups are invalid")
	}
	var selected nativeInventoryGroup
	matches := 0
	for _, raw := range inventory.Groups {
		// Inspect only the identity of unrelated entries: cleanup and candidate
		// entries can intentionally share a family but have different targets.
		var identity struct {
			Group  string `json:"group"`
			Target string `json:"target"`
		}
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &identity) != nil {
			return result, errors.New("invalid inventory group entry")
		}
		if identity.Group != groupID || identity.Target != target {
			continue
		}
		matches++
		if err := json.Unmarshal(raw, &selected); err != nil {
			return result, fmt.Errorf("invalid selected inventory group: %w", err)
		}
		result.Group = raw
	}
	if matches != 1 {
		return NativeSelection{}, errors.New("inventory must contain exactly one matching group/target")
	}
	manifests, err := validateNativeGroup(selected)
	if err != nil {
		return NativeSelection{}, err
	}
	result.BaseSHA, result.Manifests = inventory.BaseSHA, manifests
	return result, nil
}

// ValidateNativeJSON rejects duplicate fields at every depth before typed decoding.
func validateNativeJSON(decoder *json.Decoder, depth int) error {
	return validateNativeJSONValue(decoder, depth, true)
}

// ValidateNativeJSONValue checks JSON structure independently of inventory key rules.
func validateNativeJSONValue(decoder *json.Decoder, depth int, inventory bool) error {
	if depth > 256 {
		return errors.New("inventory nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON field")
			}
			if inventory {
				switch canonical := strings.ToLower(name); canonical {
				case "repository", "base_sha", "groups", "group", "target", "ecosystem", "family",
					"next_action", "action", "blockers", "sources", "number", "head_sha", "intents",
					"package", "manifest", "lock_manifests":
					if name != canonical {
						return errors.New("noncanonical inventory field")
					}
				}
			}
			seen[name] = true
			if err := validateNativeJSONValue(decoder, depth+1, inventory); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateNativeJSONValue(decoder, depth+1, inventory); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

// ValidateNativeGroup checks source identity and intent before exposing consumers.
func validateNativeGroup(group nativeInventoryGroup) ([]string, error) {
	if (group.NextAction.Action != "prepare_replacement" && group.NextAction.Action != "maintain_replacement") ||
		group.Blockers == nil || len(group.Blockers) != 0 {
		return nil, errors.New("inventory group is not eligible for replacement")
	}
	if (group.Ecosystem != "go" && group.Ecosystem != "npm" && group.Ecosystem != "actions") ||
		group.Family == "" || len(group.Sources) == 0 || len(group.Sources) > 100 {
		return nil, errors.New("invalid inventory group shape or source count")
	}
	manifests := map[string]bool{}
	var previous json.RawMessage
	for _, source := range group.Sources {
		if !nativeNumberPattern.Match(source.Number) {
			return nil, errors.New("source number must be a positive integer")
		}
		if len(source.Number) < len(previous) ||
			(len(source.Number) == len(previous) && bytes.Compare(source.Number, previous) <= 0) ||
			!commit.MatchString(source.HeadSHA) {
			return nil, errors.New("source PRs must be sorted, distinct and have full lowercase heads")
		}
		previous = source.Number
		if source.Group != group.Group || source.Target != group.Target || source.Ecosystem != group.Ecosystem ||
			source.Family != group.Family || len(source.Intents) == 0 {
			return nil, errors.New("incomplete or inconsistent source intent group/target")
		}
		familyMatches := false
		for _, intent := range source.Intents {
			if intent.Ecosystem != group.Ecosystem || intent.Package == "" {
				return nil, errors.New("source intent does not belong to selected ecosystem")
			}
			if !safeNativePath(intent.Manifest) {
				return nil, errors.New("unsafe source manifest path")
			}
			manifests[intent.Manifest] = true
			if len(intent.LockManifests) != 0 {
				var locks []string
				if err := json.Unmarshal(intent.LockManifests, &locks); err != nil || locks == nil {
					return nil, errors.New("invalid source lock manifests")
				}
				for _, lock := range locks {
					if !safeNativePath(lock) {
						return nil, errors.New("unsafe source lock manifest path")
					}
					manifests[lock] = true
				}
			}
			familyMatches = familyMatches || nativeUpgradeFamily(group.Ecosystem, intent.Package) == group.Family
		}
		if !familyMatches {
			return nil, errors.New("source lacks selected upgrade family")
		}
	}
	paths := make([]string, 0, len(manifests))
	for path := range manifests {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

// NativeUpgradeFamily preserves the native publisher's case-sensitive family identity.
func nativeUpgradeFamily(ecosystem, pkg string) string {
	if ecosystem == "actions" {
		owner, rest, found := strings.Cut(pkg, "/")
		if found {
			repository, _, _ := strings.Cut(rest, "/")
			return owner + "/" + repository
		}
	}
	if ecosystem == "npm" && (strings.HasPrefix(pkg, "@typespec/") || strings.HasPrefix(pkg, "@azure-tools/typespec-")) {
		return "typespec"
	}
	if ecosystem == "go" && strings.HasPrefix(pkg, "github.com/Azure/azure-sdk-for-go/") {
		return "github.com/Azure/azure-sdk-for-go"
	}
	return pkg
}

// SafeNativePath rejects absolute, traversal, control, whitespace and Git metadata paths.
func safeNativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") ||
		(len(path) >= 2 && path[1] == ':') {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || unicode.IsSpace(r) })
}
