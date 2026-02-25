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

	"github.com/go-logr/logr"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

type StepInspectScope func(context.Context, *types.Pipeline, string, types.Step, *InspectOptions) error

func NewStepInspectScopes(subscriptions map[string]string) map[string]StepInspectScope {
	return map[string]StepInspectScope{
		"vars": inspectVars(subscriptions),
	}
}

// InspectOptions contains the options for the Inspect method
type InspectOptions struct {
	Scope          string
	Format         string
	Step           string
	Region         string
	Configuration  configtypes.Configuration
	ScopeFunctions map[string]StepInspectScope
	OutputFile     io.Writer
	Concurrency    int

	Service     *topology.Service
	TopologyDir string
}

func Inspect(p *types.Pipeline, ctx context.Context, options *InspectOptions) error {
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.StepName() == options.Step {
				if inspectFunc, ok := options.ScopeFunctions[options.Scope]; ok {
					err := inspectFunc(ctx, p, p.ServiceGroup, step, options)
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

func inspectVars(subscriptions map[string]string) func(ctx context.Context, pipeline *types.Pipeline, serviceGroup string, s types.Step, options *InspectOptions) error {
	return func(ctx context.Context, pipeline *types.Pipeline, serviceGroup string, s types.Step, options *InspectOptions) error {
		var envVars map[string]string
		switch step := s.(type) {
		case *types.ShellStep:
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
			inputs, err := acquireOutputChainingInputs(ctx, outputChainingDependenciesList, pipeline, options, subscriptions)
			if err != nil {
				return fmt.Errorf("failure acquiring output-chaining inputs: %v", err)
			}
			envVars, err = mapStepVariables(serviceGroup, step.Variables, options.Configuration, inputs)
			if err != nil {
				return fmt.Errorf("failure mapping step variables: %v", err)
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
}

func acquireOutputChainingInputs(ctx context.Context, steps []string, pipeline *types.Pipeline, options *InspectOptions, subscriptions map[string]string) (Outputs, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	bicepClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		return nil, err
	}

	inputs := Outputs{
		pipeline.ServiceGroup: {},
	}
	for _, depStep := range steps {
		runOptions := &PipelineRunOptions{
			BaseRunOptions: BaseRunOptions{
				DryRun:                   true,
				Configuration:            options.Configuration,
				NoPersist:                true,
				DeploymentTimeoutSeconds: 60,
				BicepClient:              bicepClient,
			},
			Region:                options.Region,
			Step:                  depStep,
			SubsciptionLookupFunc: LookupSubscriptionID(subscriptions),
			Concurrency:           options.Concurrency,
			TopologyDir:           options.TopologyDir,
		}
		outputs, err := RunPipeline(options.Service, pipeline, ctx, runOptions, RunStep)
		if err != nil {
			return nil, err
		}
		for serviceGroup, resourceGroups := range outputs {
			if _, ok := inputs[serviceGroup]; !ok {
				inputs[serviceGroup] = map[string]map[string]Output{}
			}
			for group, values := range resourceGroups {
				if _, ok := inputs[serviceGroup][group]; !ok {
					inputs[serviceGroup][group] = map[string]Output{}
				}
				for key, value := range values {
					inputs[pipeline.ServiceGroup][group][key] = value
				}
			}
		}
	}
	return inputs, nil
}

func printMakefileVars(vars map[string]string, writer io.Writer) {
	for k, v := range vars {
		fmt.Fprintf(writer, "%s ?= %s\n", k, v)
	}
}

func printShellVars(vars map[string]string, writer io.Writer) {
	for k, v := range vars {
		fmt.Fprintf(writer, "export %s=\"%s\"\n", k, v)
	}
}
