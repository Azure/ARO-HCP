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

// Package semver implements the semantic-version precedence used by the current
// inventory helper, including numeric prerelease identifiers and build metadata.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
	`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Version is a parsed semantic version.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease []Identifier
	Stable     bool
	Raw        string
}

// Identifier is one dot-separated prerelease identifier. Numeric identifiers
// sort before alphanumeric ones, matching SemVer precedence.
type Identifier struct {
	Numeric bool
	Number  int
	Text    string
}

// Parses a version, rejecting leading zeros in numeric components.
func Parse(value string) (Version, error) {
	match := pattern.FindStringSubmatch(value)
	if match == nil {
		return Version{}, fmt.Errorf("invalid semantic version: %q", value)
	}
	version := Version{Raw: value, Stable: match[4] == ""}
	var err error
	if version.Major, err = strconv.Atoi(match[1]); err != nil {
		return Version{}, fmt.Errorf("invalid semantic version %q: %w", value, err)
	}
	if version.Minor, err = strconv.Atoi(match[2]); err != nil {
		return Version{}, fmt.Errorf("invalid semantic version %q: %w", value, err)
	}
	if version.Patch, err = strconv.Atoi(match[3]); err != nil {
		return Version{}, fmt.Errorf("invalid semantic version %q: %w", value, err)
	}
	if match[4] == "" {
		return version, nil
	}
	for _, item := range strings.Split(match[4], ".") {
		identifier := Identifier{Text: item}
		if isDigits(item) {
			if len(item) > 1 && strings.HasPrefix(item, "0") {
				return Version{}, fmt.Errorf("invalid numeric prerelease identifier: %q", value)
			}
			identified, err := strconv.Atoi(item)
			if err != nil {
				return Version{}, fmt.Errorf("invalid prerelease identifier %q: %w", value, err)
			}
			identifier = Identifier{Numeric: true, Number: identified}
		}
		version.Prerelease = append(version.Prerelease, identifier)
	}
	return version, nil
}

// Compare returns -1, 0 or 1 for SemVer precedence of left against right.
func Compare(left, right Version) int {
	switch {
	case left.Major != right.Major:
		return compareInt(left.Major, right.Major)
	case left.Minor != right.Minor:
		return compareInt(left.Minor, right.Minor)
	case left.Patch != right.Patch:
		return compareInt(left.Patch, right.Patch)
	case left.Stable != right.Stable:
		if left.Stable {
			return 1
		}
		return -1
	}
	for index := 0; index < len(left.Prerelease) && index < len(right.Prerelease); index++ {
		if compared := compareIdentifier(left.Prerelease[index], right.Prerelease[index]); compared != 0 {
			return compared
		}
	}
	return compareInt(len(left.Prerelease), len(right.Prerelease))
}

// CompareStrings parses and compares two versions.
func CompareStrings(left, right string) (int, error) {
	parsedLeft, err := Parse(left)
	if err != nil {
		return 0, err
	}
	parsedRight, err := Parse(right)
	if err != nil {
		return 0, err
	}
	return Compare(parsedLeft, parsedRight), nil
}

// Compares two prerelease identifiers.
func compareIdentifier(left, right Identifier) int {
	switch {
	case left.Numeric && right.Numeric:
		return compareInt(left.Number, right.Number)
	case left.Numeric:
		return -1
	case right.Numeric:
		return 1
	default:
		return strings.Compare(left.Text, right.Text)
	}
}

// Compares two integers.
func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// Reports whether a string is a non-empty run of digits.
func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
