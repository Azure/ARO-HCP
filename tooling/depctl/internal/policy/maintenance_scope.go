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

package policy

import (
	"errors"
	"regexp"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidateMaintenanceTarget rejects unsafe API paths and ambiguous revisions.
func ValidateMaintenanceTarget(repository string, number int, head string) error {
	if !repositoryPattern.MatchString(repository) {
		return errors.New("repository must be OWNER/REPO")
	}
	if number <= 0 {
		return errors.New("pull request number must be a positive integer")
	}
	if !shaPattern.MatchString(head) {
		return errors.New("expected head must be a full lowercase commit SHA")
	}
	return nil
}

// MaintenanceKinds resolves the optional maintenance inventory/action scope.
func MaintenanceKinds(kind string) ([]string, error) {
	switch kind {
	case "":
		return []string{KindUpgrade, KindDependency, KindTool}, nil
	case "non-compiler":
		return []string{KindDependency, KindTool}, nil
	case KindUpgrade, KindDependency, KindTool:
		return []string{kind}, nil
	default:
		return nil, errors.New("kind must be upgrade, dependency, tool, or non-compiler")
	}
}
