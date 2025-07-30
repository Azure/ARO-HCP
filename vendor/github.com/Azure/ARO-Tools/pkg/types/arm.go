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
	"strings"
)

// ARMStep represents an ARM deployment step.
// This struct supports fluent interface With... methods.
type ARMStep struct {
	StepMeta        `json:",inline"`
	Command         string     `json:"command,omitempty"`
	Variables       []Variable `json:"variables,omitempty"`
	Template        string     `json:"template,omitempty"`
	Parameters      string     `json:"parameters,omitempty"`
	DeploymentLevel string     `json:"deploymentLevel,omitempty"`
	OutputOnly      bool       `json:"outputOnly,omitempty"`
	DeploymentMode  string     `json:"deploymentMode,omitempty"`
}

// NewARMStep creates a new ARM deployment step with the given parameters.
//
// Parameters:
//   - name: The name of the step.
//   - template: The path to the template.
//   - parameters: The path to the parameter file.
//   - deploymentLevel: The deployment level (e.g., "ResourceGroup", "Subscription").
//
// Returns:
//   - A pointer to an ARMStep struct, representing the newly created instance.
func NewARMStep(name string, template string, parameters string, deploymentLevel string) *ARMStep {
	return &ARMStep{
		StepMeta: StepMeta{
			Name:   name,
			Action: "ARM",
		},
		Template:        template,
		Parameters:      parameters,
		DeploymentLevel: deploymentLevel,
	}
}

// WithDependsOn fluent method that sets DependsOn
func (s *ARMStep) WithDependsOn(dependsOn ...string) *ARMStep {
	s.DependsOn = dependsOn
	return s
}

// WithVariables fluent method that sets Variables
func (s *ARMStep) WithVariables(variables ...Variable) *ARMStep {
	s.Variables = variables
	return s
}

// WithOutputOnly fluent method that sets OutputOnly
func (s *ARMStep) WithOutputOnly() *ARMStep {
	s.OutputOnly = true
	return s
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
