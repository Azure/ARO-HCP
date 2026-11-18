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

// Package semver validates tool versions and compares their precedence with
// golang.org/x/mod/semver.
//
// A leading v is optional at this boundary, and a full major.minor.patch core
// is required so a short form such as v2.1 cannot become a planned target.
package semver

import (
	"fmt"
	"strings"

	modsemver "golang.org/x/mod/semver"
)

// Validate rejects anything but a full semantic version.
func Validate(value string) error {
	if _, ok := canonical(value); !ok {
		return fmt.Errorf("invalid semantic version: %q", value)
	}
	return nil
}

// IsStable reports whether a valid version carries no prerelease.
func IsStable(value string) bool {
	version, ok := canonical(value)
	return ok && modsemver.Prerelease(version) == ""
}

// CompareStrings returns -1, 0 or 1 for the precedence of left against right.
// Build metadata does not affect precedence.
func CompareStrings(left, right string) (int, error) {
	leftVersion, ok := canonical(left)
	if !ok {
		return 0, fmt.Errorf("invalid semantic version: %q", left)
	}
	rightVersion, ok := canonical(right)
	if !ok {
		return 0, fmt.Errorf("invalid semantic version: %q", right)
	}
	return modsemver.Compare(leftVersion, rightVersion), nil
}

// Canonicalizes a version for golang.org/x/mod/semver.
func canonical(value string) (string, bool) {
	trimmed := strings.TrimPrefix(value, "v")
	core := trimmed
	if index := strings.IndexAny(core, "-+"); index >= 0 {
		core = core[:index]
	}
	if strings.Count(core, ".") != 2 {
		return "", false
	}
	version := "v" + trimmed
	if !modsemver.IsValid(version) {
		return "", false
	}
	return version, true
}
