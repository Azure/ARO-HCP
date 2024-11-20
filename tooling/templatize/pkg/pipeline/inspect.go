package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

type StepInspectScope func(*step, *PipelineInspectOptions, io.Writer) error

func NewStepInspectScopes() map[string]StepInspectScope {
	return map[string]StepInspectScope{
		"vars": inspectVars,
	}
}

type PipelineInspectOptions struct {
	Aspect string
	Format string
	Step   string
	Region string
	Vars   config.Variables
}

func (p *Pipeline) Inspect(ctx context.Context, options *PipelineInspectOptions, writer io.Writer) error {
	stepInspectScopes := NewStepInspectScopes()
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.Name == options.Step {
				if inspectFunc, ok := stepInspectScopes[options.Aspect]; ok {
					err := inspectFunc(step, options, writer)
					if err != nil {
						return err
					}
				} else {
					return fmt.Errorf("unknown inspect scope %q", options.Aspect)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("step %q not found", options.Step)
}

func inspectVars(s *step, options *PipelineInspectOptions, writer io.Writer) error {
	var envVars map[string]string
	var err error
	switch s.Action {
	case "Shell":
		envVars, err = s.getEnvVars(options.Vars, false)
	default:
		return fmt.Errorf("inspecting step variables not implemented for action type %s", s.Action)
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
		fmt.Fprintf(writer, "%s ?= %s\n", k, v)
	}
}

func printShellVars(vars map[string]string, writer io.Writer) {
	for k, v := range vars {
		fmt.Fprintf(writer, "export %s=\"%s\"\n", k, v)
	}
}
