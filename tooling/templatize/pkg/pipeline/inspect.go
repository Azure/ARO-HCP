package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

type StepInspectScope func(*step, *PipelineInspectOptions) error

var stepInspectScopes = map[string]StepInspectScope{
	"vars": inspectVars,
}

func init() {
	// Initialize the map with function pointers
	stepInspectScopes["vars"] = inspectVars
}

type PipelineInspectOptions struct {
	Aspect string
	Format string
	Step   string
	Region string
	Vars   config.Variables
}

func (p *Pipeline) Inspect(ctx context.Context, options *PipelineInspectOptions) error {
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.Name == options.Step {
				if inspectFunc, ok := stepInspectScopes[options.Aspect]; ok {
					err := inspectFunc(step, options)
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

func inspectVars(s *step, options *PipelineInspectOptions) error {
	switch s.Action {
	case "Shell":
		envVars, err := s.getEnvVars(options.Vars, false)
		if err != nil {
			return err
		}
		for _, e := range envVars {
			parts := strings.SplitN(e, "=", 2)
			fmt.Printf("%s ?= %s\n", parts[0], parts[1])
		}
		return nil
	default:
		return fmt.Errorf("dumping step variables not implemented for action type %q", s.Action)
	}
}
