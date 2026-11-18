// Copyright 2025 Microsoft Corporation
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

package types

import (
	"fmt"
	"slices"
)

const StepActionShell = "Shell"

// ShellStep represents a shell step
type ShellStep struct {
	StepMeta   `json:",inline"`
	AKSCluster string      `json:"aksCluster,omitempty"`
	Command    string      `json:"command,omitempty"`
	Variables  []Variable  `json:"variables,omitempty"`
	DryRun     DryRun      `json:"dryRun,omitempty"`
	References []Reference `json:"references,omitempty"`
	SubnetName string      `json:"subnetName,omitempty"`
	// WorkingDir is the relative path from the pipeline definition to the directory which will be the *only* content
	// available to the shell during execution. If and only if this is set will a shell step be eligible for being skipped
	// during incremental execution; it is in the best interest of the author to contain the smallest amount of content
	// in the directory, as any change to any input will cause a re-run. Validation will ensure that this directory does
	// not escape the root directory of the pipeline. `$PWD` for the shell commands will be this directory.
	WorkingDir string `json:"workingDir,omitempty"`
	// ShellIdentity is the ID of the managed identity with which the shell step will execute in an Ev2 context. Required.
	ShellIdentity Value `json:"shellIdentity"`
	// AdoArtifacts is a list of Azure DevOps artifacts to download before executing the shell step.
	AdoArtifacts []AdoArtifactDownloadPipelineReference `json:"adoArtifacts,omitempty"`
	// Timeout is the maximum amount of time to wait for the shell step to complete, formatted as a duration string
	// with a single h, m, or s unit.
	// If unset, consumers should apply their default shell step timeout.
	Timeout string `json:"timeout,omitempty"`
}

// Reference represents a configurable reference
type Reference struct {
	// Environment variable name
	Name string `json:"name"`

	// The path to a file.
	FilePath string `json:"filepath"`
}

// Description
// Returns:
//   - A string representation of this ShellStep
func (s *ShellStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  Command: %s\n", s.Name, s.Action, s.Command)
}

func (s *ShellStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range append(s.Variables, s.DryRun.Variables...) {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.ShellIdentity.Input != nil {
		deps = append(deps, s.ShellIdentity.Input.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *ShellStep) IsWellFormedOverInputs() bool {
	// raw shell steps capture the whole repository as an archive input, so they are not well-formed
	return s.WorkingDir != ""
}

// ShellValidationStep represents a shell step that is a validation step.
type ShellValidationStep struct {
	ShellStep  `json:",inline"`
	Validation []string `json:"validation,omitempty"`
}

func (s *ShellValidationStep) Validations() []string {
	return s.Validation
}

func (s *ShellValidationStep) IsWellFormedOverInputs() bool {
	// raw shell steps capture the whole repository as an archive input, so they are not well-formed
	return false
}
