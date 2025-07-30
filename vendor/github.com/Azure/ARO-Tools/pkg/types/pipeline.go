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

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"
	types2 "github.com/Azure/ARO-Tools/pkg/config/types"
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
	bytes, err := config.PreprocessFile(pipelineFilePath, cfg)
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

	if err = pipeline.Validate(); err != nil {
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
	// collect all steps from all resourcegroups and fail if there are duplicates
	stepMap := make(map[string]Step)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if _, ok := stepMap[step.StepName()]; ok {
				return fmt.Errorf("duplicate step name %q", step.StepName())
			}
			stepMap[step.StepName()] = step
		}
	}

	// validate dependsOn for a step exists
	for _, step := range stepMap {
		for _, dep := range step.Dependencies() {
			if _, ok := stepMap[dep]; !ok {
				return fmt.Errorf("invalid dependency on step %s: dependency %s does not exist", step.StepName(), dep)
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
