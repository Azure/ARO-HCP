package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/ARO-Tools/pkg/config"
)

type StepInspectScope func(context.Context, *Pipeline, Step, *InspectOptions, io.Writer) error

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
	Vars           config.Variables
	ScopeFunctions map[string]StepInspectScope
}

// NewInspectOptions creates a new PipelineInspectOptions struct
func NewInspectOptions(vars config.Variables, region, step, scope, format string) *InspectOptions {
	return &InspectOptions{
		Scope:          scope,
		Format:         format,
		Step:           step,
		Region:         region,
		Vars:           vars,
		ScopeFunctions: NewStepInspectScopes(),
	}
}

func (p *Pipeline) Inspect(ctx context.Context, options *InspectOptions, writer io.Writer) error {
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.StepName() == options.Step {
				if inspectFunc, ok := options.ScopeFunctions[options.Scope]; ok {
					err := inspectFunc(ctx, p, step, options, writer)
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

func inspectVars(ctx context.Context, pipeline *Pipeline, s Step, options *InspectOptions, writer io.Writer) error {
	var err error

	// dry-run the step dependencies to get their outputs for output chaining
	inputs := make(map[string]output)
	for _, depStep := range s.Dependencies() {
		runOptions := &PipelineRunOptions{
			DryRun:                   true,
			Vars:                     options.Vars,
			Region:                   options.Region,
			Step:                     depStep,
			SubsciptionLookupFunc:    LookupSubscriptionID,
			NoPersist:                true,
			DeploymentTimeoutSeconds: 60,
			StdoutQuiet:              true,
		}
		outputs, err := RunPipeline(pipeline, ctx, runOptions)
		if err != nil {
			return err
		}
		for key, value := range outputs {
			inputs[key] = value
		}
	}

	var envVars map[string]string
	switch step := s.(type) {
	case *ShellStep:
		envVars, err = step.mapStepVariables(options.Vars, inputs)
	default:
		return fmt.Errorf("inspecting step variables not implemented for action type %s", s.ActionType())
	}
	if err != nil {
		return err
	}

	switch options.Format {
	case "makefile":
		printMakefileVars(envVars, writer)
	case "shell":
		printShellVars(envVars, writer)
	default:
		return fmt.Errorf("unknown output format %q", options.Format)
	}
	return nil
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
