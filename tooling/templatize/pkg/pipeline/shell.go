package pipeline

import (
	"context"
	"fmt"
	"maps"
	"os/exec"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/utils"
)

func (s *step) createCommand(ctx context.Context, dryRun bool, envVars map[string]string) (*exec.Cmd, bool) {
	var cmd *exec.Cmd
	if dryRun {
		if s.DryRun.Command == nil && s.DryRun.EnvVars == nil {
			return nil, true
		}
		for _, e := range s.DryRun.EnvVars {
			envVars[e.Name] = e.Value
		}
		if s.DryRun.Command != nil {
			cmd = exec.CommandContext(ctx, s.DryRun.Command[0], s.DryRun.Command[1:]...)
		}
	}
	if cmd == nil {
		// if dry-run is not enabled, use the actual command or also if no dry-run command is defined
		cmd = exec.CommandContext(ctx, s.Command[0], s.Command[1:]...)
	}
	cmd.Env = append(cmd.Env, utils.MapToEnvVarArray(envVars)...)
	return cmd, false
}

func (s *step) runShellStep(ctx context.Context, kubeconfigFile string, options *PipelineRunOptions) error {
	if s.outputFunc == nil {
		s.outputFunc = func(output string) {
			fmt.Println(output)
		}
	}

	logger := logr.FromContextOrDiscard(ctx)

	// build ENV vars
	stepVars, err := s.mapStepVariables(options.Vars)
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	envVars := utils.GetOsVariable()

	maps.Copy(envVars, stepVars)
	// execute the command
	cmd, skipCommand := s.createCommand(ctx, options.DryRun, envVars)
	if skipCommand {
		logger.V(5).Info("Skipping step '%s' due to missing dry-run configuiration", s.Name)
		return nil
	}

	if kubeconfigFile != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", kubeconfigFile))
	}

	logger.V(5).Info(fmt.Sprintf("Executing shell command: %s\n", s.Command), "command", s.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}

	s.outputFunc(string(output))

	return nil
}

func (s *step) mapStepVariables(vars config.Variables) (map[string]string, error) {
	envVars := make(map[string]string)
	for _, e := range s.Env {
		value, found := vars.GetByPath(e.ConfigRef)
		if !found {
			return nil, fmt.Errorf("failed to lookup config reference %s for %s", e.ConfigRef, e.Name)
		}
		envVars[e.Name] = utils.AnyToString(value)
	}
	return envVars, nil
}
