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
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ErrGuard reports a refused publication or retest action.
var ErrGuard = errors.New("dependabot guard refused action")

// Candidate is a validated dependency-group proposal.
type Candidate struct {
	Ecosystem string
	Family    string
	Version   string
	Manifests map[string]bool
	Title     string
	rawFamily string
}

var (
	familyPattern  = regexp.MustCompile(`^[@a-zA-Z0-9._/+*-]{1,100}$`)
	versionPattern = regexp.MustCompile(`^v?[0-9][a-zA-Z0-9.+_-]{0,50}$`)
	pathPattern    = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)
	diffGitLine    = regexp.MustCompile(`(?m)^diff --git a/(.*) b/.*$`)
)

// Group validates one proposal's structured data and derives its title.
func Group(item map[string]any) (Candidate, error) {
	raw, ok := item["data"].(map[string]any)
	if !ok {
		return Candidate{}, fmt.Errorf("%w: create_pull_request requires dependency group data", ErrGuard)
	}
	ecosystem, _ := raw["ecosystem"].(string)
	family, _ := raw["package_family"].(string)
	version, _ := raw["target_version"].(string)
	if ecosystem != "go" && ecosystem != "npm" {
		return Candidate{}, fmt.Errorf("%w: invalid dependency group metadata", ErrGuard)
	}
	if !familyPattern.MatchString(family) || !versionPattern.MatchString(version) {
		return Candidate{}, fmt.Errorf("%w: invalid dependency group metadata", ErrGuard)
	}
	manifests := map[string]bool{}
	manifestList, ok := raw["manifests"].([]any)
	if !ok || len(manifestList) == 0 {
		return Candidate{}, fmt.Errorf("%w: invalid dependency group metadata", ErrGuard)
	}
	for _, entry := range manifestList {
		value, ok := entry.(string)
		if !ok {
			return Candidate{}, fmt.Errorf("%w: invalid dependency group metadata", ErrGuard)
		}
		value = path.Clean(value)
		if !pathPattern.MatchString(value) || strings.HasPrefix(value, "/") ||
			containsDotDot(value) || !isManifestName(value) {
			return Candidate{}, fmt.Errorf("%w: invalid dependency group metadata", ErrGuard)
		}
		manifests[value] = true
	}
	actual, err := actualManifests(item)
	if err != nil {
		return Candidate{}, err
	}
	if len(actual) > 0 && !sameSet(actual, manifests) {
		return Candidate{}, fmt.Errorf("%w: manifests in data do not match actual manifests in patch", ErrGuard)
	}
	return Candidate{
		Ecosystem: ecosystem, Family: strings.ToLower(family), Version: strings.ToLower(version),
		Manifests: manifests, Title: family + " to " + version, rawFamily: family,
	}, nil
}

// actualManifests derives changed manifests from files or a patch artifact.
func actualManifests(item map[string]any) (map[string]bool, error) {
	actual := map[string]bool{}
	if files, ok := item["files"].([]any); ok {
		for _, entry := range files {
			file, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			value, ok := file["path"].(string)
			if !ok {
				continue
			}
			actual[path.Clean(value)] = true
		}
	} else if patch, ok := item["patch"].(string); ok {
		for _, match := range diffGitLine.FindAllStringSubmatch(patch, -1) {
			actual[path.Clean(match[1])] = true
		}
	}
	result := map[string]bool{}
	for file := range actual {
		if isManifestName(file) {
			result[file] = true
		}
	}
	return result, nil
}

// IsManifestName reports whether the basename is a dependency manifest.
func isManifestName(file string) bool {
	base := path.Base(file)
	return base == "go.mod" || base == "package.json"
}

// containsDotDot reports whether any path segment is a parent reference.
func containsDotDot(file string) bool {
	for _, part := range strings.Split(file, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// sameSet reports whether two string sets are equal.
func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

// FamilyMatches applies the prompt's module-prefix and glob family semantics.
func FamilyMatches(family, pkg string) bool {
	pkg = strings.ToLower(pkg)
	family = strings.ToLower(family)
	if globMatch(family, pkg) {
		return true
	}
	if strings.HasSuffix(family, "/*") && pkg == strings.TrimSuffix(family, "/*") {
		return true
	}
	if strings.HasPrefix(pkg, strings.TrimSuffix(family, "/")+"/") {
		return true
	}
	return !strings.Contains(family, "/") && strings.HasPrefix(pkg, "@"+family+"/")
}

// globMatch translates shell-style globs into an anchored regular expression;
// unlike path.Match, '*' also crosses '/'.
func globMatch(pattern, value string) bool {
	var builder strings.Builder
	builder.WriteString(`(?s)^`)
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			builder.WriteString(`.*`)
		case '?':
			builder.WriteString(`.`)
		case '[':
			end := strings.IndexByte(pattern[index:], ']')
			if end < 0 {
				builder.WriteString(`\[`)
				continue
			}
			builder.WriteString(pattern[index : index+end+1])
			index += end
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	builder.WriteString(`$`)
	matched, err := regexp.MatchString(builder.String(), value)
	return err == nil && matched
}

// ErrTruncatedPatch reports an inconsistent unified diff.
var ErrTruncatedPatch = errors.New("patch is truncated")

// Hunk is one unified-diff hunk with its old and new starting lines.
type Hunk struct {
	OldStart int
	NewStart int
	Lines    [][2]string
}

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@.*$`)

// PatchHunks validates a file's unified diff before trusting any inference.
func PatchHunks(file map[string]any) ([]Hunk, error) {
	patch, ok := file["patch"].(string)
	if !ok || patch == "" {
		return nil, errors.New("patch is unavailable")
	}
	status, _ := file["status"].(string)
	switch status {
	case "added", "modified", "removed", "renamed", "copied":
	default:
		return nil, errors.New("file status is unavailable or unsupported")
	}
	var hunks []Hunk
	oldLeft, newLeft := 0, 0
	additions, deletions := 0, 0
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for _, line := range lines {
		if match := hunkHeader.FindStringSubmatch(line); match != nil {
			if oldLeft != 0 || newLeft != 0 {
				return nil, ErrTruncatedPatch
			}
			oldCount, newCount := 1, 1
			if match[2] != "" {
				parsed, err := strconv.Atoi(match[2])
				if err != nil {
					return nil, fmt.Errorf("%w: invalid hunk count", ErrTruncatedPatch)
				}
				oldCount = parsed
			}
			if match[4] != "" {
				parsed, err := strconv.Atoi(match[4])
				if err != nil {
					return nil, fmt.Errorf("%w: invalid hunk count", ErrTruncatedPatch)
				}
				newCount = parsed
			}
			oldStart, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("%w: invalid hunk start", ErrTruncatedPatch)
			}
			newStart, err := strconv.Atoi(match[3])
			if err != nil {
				return nil, fmt.Errorf("%w: invalid hunk start", ErrTruncatedPatch)
			}
			oldLeft, newLeft = oldCount, newCount
			hunks = append(hunks, Hunk{OldStart: oldStart, NewStart: newStart})
			continue
		}
		if line == `\ No newline at end of file` {
			continue
		}
		if len(hunks) > 0 && len(line) > 0 && strings.ContainsRune(" +-", rune(line[0])) {
			marker := line[0]
			if marker != '+' {
				oldLeft--
			}
			if marker != '-' {
				newLeft--
			}
			if marker == '+' {
				additions++
			}
			if marker == '-' {
				deletions++
			}
			if oldLeft < 0 || newLeft < 0 {
				return nil, errors.New("patch has inconsistent hunk counts")
			}
			hunks[len(hunks)-1].Lines = append(hunks[len(hunks)-1].Lines, [2]string{string(marker), line[1:]})
			continue
		}
		return nil, errors.New("patch is not a unified diff")
	}
	if len(hunks) == 0 || oldLeft != 0 || newLeft != 0 {
		return nil, ErrTruncatedPatch
	}
	if field, ok := file["additions"].(float64); ok && int(field) != additions {
		return nil, errors.New("patch additions count is incomplete")
	}
	if field, ok := file["deletions"].(float64); ok && int(field) != deletions {
		return nil, errors.New("patch deletions count is incomplete")
	}
	return hunks, nil
}

// sideEntries tracks package sets per dependency entry on each diff side.
type sideEntries struct {
	minus map[string]map[string]bool
	plus  map[string]map[string]bool
}

// newSideEntries returns empty diff-side state.
func newSideEntries() *sideEntries {
	return &sideEntries{minus: map[string]map[string]bool{}, plus: map[string]map[string]bool{}}
}

// set records packages under one entry key for a marker side.
func (s *sideEntries) set(side, key string, packages ...string) {
	target := s.minus
	if side == "+" {
		target = s.plus
	}
	entry := target[key]
	if entry == nil {
		entry = map[string]bool{}
		target[key] = entry
	}
	for _, pkg := range packages {
		entry[pkg] = true
	}
}

// changedPackages unions packages of entries present on only one side.
func (s *sideEntries) changedPackages() map[string]bool {
	result := map[string]bool{}
	for key, packages := range s.minus {
		if _, ok := s.plus[key]; !ok {
			for pkg := range packages {
				result[pkg] = true
			}
		}
	}
	for key, packages := range s.plus {
		if _, ok := s.minus[key]; !ok {
			for pkg := range packages {
				result[pkg] = true
			}
		}
	}
	return result
}

// GoDependencies extracts changed Go module packages from diff hunks.
func GoDependencies(hunks []Hunk) (map[string]bool, error) {
	entries := newSideEntries()
	module := `(?:"[a-zA-Z0-9._~+/-]+"|[a-zA-Z0-9._~+/-]+)`
	version := `v[0-9][a-zA-Z0-9.+_-]*`
	requireDirective := regexp.MustCompile(`^(require|replace|exclude|retract|godebug)\s*\($`)
	directiveLine := regexp.MustCompile(`^(require|replace|exclude)\s+`)
	ignoreLine := regexp.MustCompile(`^(go|toolchain|module|godebug|retract)\s+`)
	modulePattern := regexp.MustCompile(fmt.Sprintf(`^(%s)(?:\s+(%s))?$`, module, version))
	replacePattern := regexp.MustCompile(fmt.Sprintf(`^(%s)\s+(%s)$`, module, version))
	replaceTarget := regexp.MustCompile(`^(?:"[^"]+"|[./][^\s]+)$`)
	requirePattern := regexp.MustCompile(fmt.Sprintf(`^(%s)\s+(%s)$`, module, version))
	for _, hunk := range hunks {
		block := ""
		for _, side := range []string{"-", "+"} {
			for _, pair := range hunk.Lines {
				marker, text := pair[0], pair[1]
				if marker != " " && marker != side {
					continue
				}
				text = strings.TrimSpace(strings.SplitN(text, "//", 2)[0])
				if match := requireDirective.FindStringSubmatch(text); match != nil {
					block = match[1]
					continue
				}
				if text == ")" {
					block = ""
					continue
				}
				if marker != side || text == "" || ignoreLine.MatchString(text) || block == "retract" || block == "godebug" {
					continue
				}
				directive := block
				if directive == "" {
					directive = "require"
				}
				if match := directiveLine.FindStringSubmatch(text); match != nil {
					directive = match[1]
				}
				text = directiveLine.ReplaceAllString(text, "")
				key := ""
				if strings.Contains(text, "=>") {
					before, after, _ := strings.Cut(text, "=>")
					match := modulePattern.FindStringSubmatch(strings.TrimSpace(before))
					replacement := replacePattern.FindStringSubmatch(strings.TrimSpace(after))
					if match == nil || (replacement == nil && !replaceTarget.MatchString(strings.TrimSpace(after))) {
						return nil, errors.New("cannot classify Go replace change")
					}
					pkg := strings.Trim(match[1], `"`)
					target := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(after), `"`), `"`)
					var targetVersion *string
					if replacement != nil {
						target = strings.Trim(replacement[1], `"`)
						version := replacement[2]
						targetVersion = &version
					}
					targetText := ""
					if targetVersion != nil {
						targetText = *targetVersion
					}
					key = fmt.Sprintf("replace\x00%s\x00%s\x00%s\x00%s", pkg, match[2], target, targetText)
					entries.set(side, key, pkg)
					if replacement != nil {
						entries.set(side, key, target)
					}
					continue
				}
				match := requirePattern.FindStringSubmatch(text)
				if match == nil {
					return nil, errors.New("cannot classify Go manifest change")
				}
				pkg := strings.Trim(match[1], `"`)
				key = fmt.Sprintf("%s\x00%s\x00%s", directive, pkg, match[2])
				entries.set(side, key, pkg)
			}
		}
	}
	return entries.changedPackages(), nil
}
