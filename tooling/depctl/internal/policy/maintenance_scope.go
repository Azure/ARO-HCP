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
