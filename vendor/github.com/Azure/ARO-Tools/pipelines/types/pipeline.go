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
	"os"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	types2 "github.com/Azure/ARO-Tools/config/types"
)

type Pipeline struct {
	Schema         string           `json:"$schema,omitempty"`
	ServiceGroup   string           `json:"serviceGroup"`
	RolloutName    string           `json:"rolloutName"`
	ResourceGroups []*ResourceGroup `json:"resourceGroups"`
	BuildStep      *BuildStep       `json:"buildStep,omitempty"`
}

// BuildStep describes how artifacts should be built before any shell steps are run. The command specified here
// will run with the working directory set to the directory holding this pipeline specification.
type BuildStep struct {
	// Command is the command to run for the build step.
	Command string `json:"command"`

	// Args are the command-line arguments to pass to the build step.
	Args []string `json:"args"`
}

// NewPipelineFromFile prepocesses and creates a new Pipeline instance from a file.
//
// Parameters:
//   - pipelineFilePath: The path to the pipeline file.
//   - cfg: The configuration object used for preprocessing the file.
//
// Returns:
//   - A pointer to a new Pipeline instance if successful.
//   - An error if there was a problem preprocessing the file, validating the schema,
//     unmarshaling the pipeline, or validating the pipeline instance.
func NewPipelineFromFile(pipelineFilePath string, cfg types2.Configuration) (*Pipeline, error) {
	content, err := os.ReadFile(pipelineFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", pipelineFilePath, err)
	}

	return NewPipelineFromBytes(content, cfg)
}

func NewPipelineFromBytes(pipelineBytes []byte, cfg types2.Configuration) (*Pipeline, error) {
	bytes, err := config.PreprocessContent(pipelineBytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess pipeline file: %w", err)
	}

	if err := ValidatePipelineSchema(bytes); err != nil {
		return nil, fmt.Errorf("failed to validate pipeline schema: %w", err)
	}

	var pipeline Pipeline
	if err := yaml.Unmarshal(bytes, &pipeline); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline file: %w", err)
	}

	if err := pipeline.Validate(); err != nil {
		return nil, fmt.Errorf("pipeline file failed validation: %w", err)
	}

	return &pipeline, nil
}

// Validate checks the integrity of the pipeline and its resource groups.
// It ensures that there are no duplicate step names, that all dependencies exist,
// and that each resource group is valid.
//
// Returns:
//   - An error if the pipeline or any of its resource groups are invalid.
//   - nil if the pipeline and all its resource groups are valid.
func (p *Pipeline) Validate() error {
	groups := sets.New[string]()
	references := map[string]sets.Set[string]{}
	for i, rg := range p.ResourceGroups {
		if groups.Has(rg.Name) {
			return fmt.Errorf("pipeline.resourceGroups[%d:%s]: resource group name %q duplicated", i, rg.Name, rg.Name)
		}
		groups.Insert(rg.Name)

		steps := sets.New[string]()
		for j, step := range rg.Steps {
			if steps.Has(step.StepName()) {
				return fmt.Errorf("pipeline.resourceGroups[%d:%s].steps[%d:%s]: step name %q duplicated", i, rg.Name, j, step.StepName(), step.StepName())
			}
			steps.Insert(step.StepName())
		}
		references[rg.Name] = steps

		serviceValidationSteps := sets.New[string]()
		for j, step := range rg.ValidationSteps {
			if steps.Has(step.StepName()) {
				return fmt.Errorf("pipeline.resourceGroups[%d:%s].serviceValidationSteps[%d:%s]: step name %q duplicated with a regular step", i, rg.Name, j, step.StepName(), step.StepName())
			}
			if steps.Has(step.StepName()) {
				return fmt.Errorf("pipeline.resourceGroups[%d:%s].serviceValidationSteps[%d:%s]: step name %q duplicated", i, rg.Name, j, step.StepName(), step.StepName())
			}
			serviceValidationSteps.Insert(step.StepName())
		}
	}

	for i, rg := range p.ResourceGroups {
		for j, step := range rg.Steps {
			for _, dep := range append(step.Dependencies(), step.RequiredInputs()...) {
				group, exists := references[dep.ResourceGroup]
				if !exists {
					return fmt.Errorf("pipeline.resourceGroups[%d:%s].steps[%d:%s]: dependency %s/%s invalid: no such resource group %s", i, rg.Name, j, step.StepName(), dep.ResourceGroup, dep.Step, dep.ResourceGroup)
				}
				if !group.Has(dep.Step) {
					return fmt.Errorf("pipeline.resourceGroups[%d:%s].steps[%d:%s]: dependency %s/%s invalid: resource group %s has no step %s", i, rg.Name, j, step.StepName(), dep.ResourceGroup, dep.Step, dep.ResourceGroup, dep.Step)
				}
			}
		}
	}

	// todo check for circular dependencies

	// validate resource groups
	for _, rg := range p.ResourceGroups {
		err := rg.Validate()
		if err != nil {
			return err
		}
	}
	return nil
}
