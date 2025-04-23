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

package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/ARO-Tools/pkg/config"
)

type StepInspectScope func(context.Context, *Pipeline, Step, *InspectOptions) error

func NewStepInspectScopes() map[string]StepInspectScope {
	return map[string]StepInspectScope{
		"vars": inspectVars,
	}
}

// InspectOptions contains the options for the Inspect method
type InspectOptions struct {
	Scope          string
	Format         string
	Step           string
	Region         string
	Configuration  config.Configuration
	ScopeFunctions map[string]StepInspectScope
	OutputFile     io.Writer
}

// NewInspectOptions creates a new PipelineInspectOptions struct
func NewInspectOptions(cfg config.Configuration, region, step, scope, format string, outputFile io.Writer) *InspectOptions {
	return &InspectOptions{
		Scope:          scope,
		Format:         format,
		Step:           step,
		Region:         region,
		Configuration:  cfg,
		ScopeFunctions: NewStepInspectScopes(),
		OutputFile:     outputFile,
	}
}

func (p *Pipeline) Inspect(ctx context.Context, options *InspectOptions) error {
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.StepName() == options.Step {
				if inspectFunc, ok := options.ScopeFunctions[options.Scope]; ok {
					err := inspectFunc(ctx, p, step, options)
					if err != nil {
						return err
					}
				} else {
					return fmt.Errorf("unknown inspect scope %q", options.Scope)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("step %q not found", options.Step)
}

func inspectVars(ctx context.Context, pipeline *Pipeline, s Step, options *InspectOptions) error {
	var envVars map[string]string
	switch step := s.(type) {
	case *ShellStep:
		outputChainingDependencies := make(map[string]bool)
		for _, stepVar := range step.Variables {
			if stepVar.Input != nil && stepVar.Input.Step != "" {
				outputChainingDependencies[stepVar.Input.Step] = true
			}
		}
		outputChainingDependenciesList := make([]string, 0, len(outputChainingDependencies))
		for depStep := range outputChainingDependencies {
			outputChainingDependenciesList = append(outputChainingDependenciesList, depStep)
		}
		inputs, err := aquireOutputChainingInputs(ctx, outputChainingDependenciesList, pipeline, options)
		if err != nil {
			return err
		}
		envVars, err = step.mapStepVariables(options.Configuration, inputs)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("inspecting step variables not implemented for action type %s", s.ActionType())
	}

	switch options.Format {
	case "makefile":
		printMakefileVars(envVars, options.OutputFile)
	case "shell":
		printShellVars(envVars, options.OutputFile)
	default:
		return fmt.Errorf("unknown output format %q", options.Format)
	}
	return nil
}

func aquireOutputChainingInputs(ctx context.Context, steps []string, pipeline *Pipeline, options *InspectOptions) (map[string]output, error) {
	inputs := make(map[string]output)
	for _, depStep := range steps {
		runOptions := &PipelineRunOptions{
			DryRun:                   true,
			Configuration:            options.Configuration,
			Region:                   options.Region,
			Step:                     depStep,
			SubsciptionLookupFunc:    LookupSubscriptionID,
			NoPersist:                true,
			DeploymentTimeoutSeconds: 60,
		}
		outputs, err := RunPipeline(pipeline, ctx, runOptions)
		if err != nil {
			return nil, err
		}
		for key, value := range outputs {
			inputs[key] = value
		}
	}
	return inputs, nil
}

func printMakefileVars(vars map[string]string, writer io.Writer) {
	for k, v := range vars {
		fmt.Fprintf(writer, "%s ?= \"%s\"\n", k, v)
	}
}

func printShellVars(vars map[string]string, writer io.Writer) {
	for k, v := range vars {
		fmt.Fprintf(writer, "export %s=\"%s\"\n", k, v)
	}
}
