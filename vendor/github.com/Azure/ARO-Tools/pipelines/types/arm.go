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
	"strings"
)

const StepActionARM = "ARM"

// ARMStep represents an ARM deployment step.
type ARMStep struct {
	StepMeta        `json:",inline"`
	Variables       []Variable `json:"variables,omitempty"`
	Template        string     `json:"template,omitempty"`
	Parameters      string     `json:"parameters,omitempty"`
	DeploymentLevel string     `json:"deploymentLevel,omitempty"`
	OutputOnly      bool       `json:"outputOnly,omitempty"`
	DeploymentMode  string     `json:"deploymentMode,omitempty"`
}

// Description
// Returns:
//   - A string representation of this ShellStep
func (s *ARMStep) Description() string {
	var details []string
	details = append(details, fmt.Sprintf("Template: %s", s.Template))
	details = append(details, fmt.Sprintf("Parameters: %s", s.Parameters))
	return fmt.Sprintf("Step %s\n  Kind: %s\n  %s", s.Name, s.Action, strings.Join(details, "\n  "))
}

func (s *ARMStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range s.Variables {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionARMStack = "ARMStack"

const (
	// ARMStackOperationCreateOrUpdate deploys the template into the stack. It is the default when no operation is set.
	ARMStackOperationCreateOrUpdate = "createOrUpdate"
	// ARMStackOperationDelete removes an existing stack. It succeeds without changes when the stack does not exist.
	ARMStackOperationDelete = "delete"
)

// ARMStackStep represents an ARM stack deployment step.
type ARMStackStep struct {
	StepMeta        `json:",inline"`
	Variables       []Variable `json:"variables,omitempty"`
	Template        string     `json:"template,omitempty"`
	Parameters      string     `json:"parameters,omitempty"`
	DeploymentLevel string     `json:"deploymentLevel,omitempty"`

	// Operation is either 'createOrUpdate' (the default when empty) or 'delete'. With 'delete', the step removes the
	// stack named by DeploymentStackNameForDelete instead of deploying a template.
	Operation string `json:"operation,omitempty"`

	// ActionOnUnmanage specifies what action to take when a resource previously owned by the deployment stack is no longer
	// specified, or, with operation 'delete', what happens to every resource the removed stack manages. 'delete' will
	// delete the resource, 'detach' will leave it as-is.
	ActionOnUnmanage string `json:"actionOnUnmanage,omitempty"`
	// BypassStackOutOfSyncError allows bypassing service errors that indicate the stack resource list is not correctly synchronized.
	BypassStackOutOfSyncError bool `json:"bypassStackOutOfSyncError,omitempty"`

	// DeploymentStackNameForDelete is the name of the existing stack to delete, exactly as Azure shows it, in the resource
	// group this step runs in. Only used with operation 'delete'; create and update always derive the name from the step.
	DeploymentStackNameForDelete *Value `json:"deploymentStackNameForDelete,omitempty"`
	// Timeout bounds a delete, as a Go duration string. The tool's default applies when unset.
	Timeout string `json:"timeout,omitempty"`
	// IdentityFrom specifies the managed identity with which a delete runs in Ev2.
	IdentityFrom *Input `json:"identityFrom,omitempty"`
}

// IsDelete reports whether the step removes a stack rather than deploying one.
func (s *ARMStackStep) IsDelete() bool {
	return s.Operation == ARMStackOperationDelete
}

// Description
// Returns:
//   - A string representation of this ARMStackStep
func (s *ARMStackStep) Description() string {
	var details []string
	if s.IsDelete() {
		details = append(details, fmt.Sprintf("Operation: %s", s.Operation))
		if s.DeploymentStackNameForDelete != nil {
			details = append(details, fmt.Sprintf("DeploymentStackNameForDelete: %s", s.DeploymentStackNameForDelete.String()))
		}
		details = append(details, fmt.Sprintf("ActionOnUnmanage: %s", s.ActionOnUnmanage))
	} else {
		details = append(details, fmt.Sprintf("Template: %s", s.Template))
		details = append(details, fmt.Sprintf("Parameters: %s", s.Parameters))
	}
	return fmt.Sprintf("Step %s\n  Kind: %s\n  %s", s.Name, s.Action, strings.Join(details, "\n  "))
}

func (s *ARMStackStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range s.Variables {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.DeploymentStackNameForDelete != nil && s.DeploymentStackNameForDelete.Input != nil {
		deps = append(deps, s.DeploymentStackNameForDelete.Input.StepDependency)
	}
	if s.IdentityFrom != nil {
		deps = append(deps, s.IdentityFrom.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}
