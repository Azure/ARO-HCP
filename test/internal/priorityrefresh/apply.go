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

package priorityrefresh

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Apply publishes every planned file replacement. Each file is re-hashed first,
// and a failure restores the files already replaced where possible.
func Apply(changes ChangeSet) error {
	if changes.repo == "" {
		return fmt.Errorf("change set has no checkout path; plan the changes before applying them")
	}
	var replaced []writtenFile
	for _, file := range changes.files {
		// The checkout boundary is re-checked at publish time: a symlink created
		// after planning must not redirect the write outside the repository.
		if err := ensureNoSymlinkComponents(changes.repo, file.context.absPath); err != nil {
			return restore(replaced, err)
		}
		current, err := os.ReadFile(file.context.absPath)
		if err != nil {
			return restore(replaced, fmt.Errorf("read %s before writing: %w", file.context.relPath, err))
		}
		if sha256.Sum256(current) != file.context.digest {
			return restore(replaced, fmt.Errorf("%s changed since planning; refusing to write", file.context.relPath))
		}
		if err := writeFile(file.context.absPath, file.replacement, file.context.mode); err != nil {
			return restore(replaced, err)
		}
		replaced = append(replaced, writtenFile{
			path:      file.context.absPath,
			original:  current,
			published: sha256.Sum256(file.replacement),
			mode:      file.context.mode,
		})
	}
	return nil
}

// writeFile publishes a same-directory temporary file over the target.
func writeFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".test-priorities-*.go")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set mode on temporary file for %s: %w", path, err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// writtenFile records a replaced file and the content that was published, so a
// failure can restore it only while nobody else has edited the file.
type writtenFile struct {
	path      string
	original  []byte
	published [sha256.Size]byte
	mode      os.FileMode
}

// restore puts back the originals of already replaced files after a failure.
func restore(replaced []writtenFile, cause error) error {
	var failures []string
	for _, file := range replaced {
		current, err := os.ReadFile(file.path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s needs recovery: %v", file.path, err))
			continue
		}
		if sha256.Sum256(current) != file.published {
			failures = append(failures, fmt.Sprintf("%s needs recovery: changed after publication", file.path))
			continue
		}
		if err := writeFile(file.path, file.original, file.mode); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w (also failed to restore: %s)", cause, strings.Join(failures, "; "))
	}
	return cause
}

// Verify compares the rebuilt catalog against the plan without consulting the
// already-modified sources: only the catalog and the proposal are authoritative.
func Verify(before, after et.ExtensionTestSpecs, changes ChangeSet) error {
	expected := make(map[string]int, len(before))
	beforeLabels := make(map[string]map[string]struct{}, len(before))
	expectedPriorities := make(map[string]map[string]struct{}, len(before))
	for _, spec := range before {
		if _, exists := expected[spec.Name]; exists {
			return fmt.Errorf("catalog contains duplicate spec name %q", spec.Name)
		}
		priority, err := specPriority(spec)
		if err != nil {
			return err
		}
		expected[spec.Name] = priority
		beforeLabels[spec.Name] = nonPriorityLabels(spec)
		expectedPriorities[spec.Name] = priorityLabels(spec)
	}
	for _, change := range changes.changes {
		if _, ok := expected[change.Name]; !ok {
			return fmt.Errorf("planned change %q is absent from the before catalog", change.Name)
		}
		expected[change.Name] = change.NewPriority
		// A zero proposal removes the label; any other value leaves exactly the
		// label the planner wrote, so an explicit zero label is a real change.
		expectedPriorities[change.Name] = map[string]struct{}{}
		if change.NewPriority != 0 {
			expectedPriorities[change.Name][labels.StartPriorityLabelPrefix+strconv.Itoa(change.NewPriority)] = struct{}{}
		}
	}

	afterSeen := make(map[string]struct{}, len(after))
	for _, spec := range after {
		if _, ok := afterSeen[spec.Name]; ok {
			return fmt.Errorf("after catalog contains duplicate spec name %q", spec.Name)
		}
		afterSeen[spec.Name] = struct{}{}
		want, ok := expected[spec.Name]
		if !ok {
			return fmt.Errorf("after catalog gained unexpected spec %q", spec.Name)
		}
		priority, err := specPriority(spec)
		if err != nil {
			return err
		}
		if priority != want {
			return fmt.Errorf("spec %q has priority %d after the refresh, want %d", spec.Name, priority, want)
		}
		if got := nonPriorityLabels(spec); !equalSets(got, beforeLabels[spec.Name]) {
			return fmt.Errorf("spec %q changed labels other than StartPriority", spec.Name)
		}
		if got := priorityLabels(spec); !equalSets(got, expectedPriorities[spec.Name]) {
			return fmt.Errorf("spec %q declares start priorities %v after the refresh, want %v",
				spec.Name, labelNames(got), labelNames(expectedPriorities[spec.Name]))
		}
	}
	if len(afterSeen) != len(expected) {
		var missing []string
		for name := range expected {
			if _, ok := afterSeen[name]; !ok {
				missing = append(missing, name)
			}
		}
		return fmt.Errorf("after catalog is missing %d specs: %s", len(missing), strings.Join(sortedStrings(missing), ", "))
	}
	return nil
}

// priorityLabels returns the spec's start-priority labels exactly as declared.
func priorityLabels(spec *et.ExtensionTestSpec) map[string]struct{} {
	result := map[string]struct{}{}
	for label := range spec.Labels {
		if strings.HasPrefix(label, labels.StartPriorityLabelPrefix) {
			result[label] = struct{}{}
		}
	}
	return result
}

// labelNames returns sorted label names from a label set.
func labelNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	return sortedStrings(names)
}

// nonPriorityLabels returns a spec's labels without its start priority.
func nonPriorityLabels(spec *et.ExtensionTestSpec) map[string]struct{} {
	result := make(map[string]struct{}, spec.Labels.Len())
	for label := range spec.Labels {
		if strings.HasPrefix(label, labels.StartPriorityLabelPrefix) {
			continue
		}
		result[label] = struct{}{}
	}
	return result
}

// equalSets compares two label sets.
func equalSets(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}
