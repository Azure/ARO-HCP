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

func (s *Step) createCommand(ctx context.Context, dryRun bool, envVars map[string]string) (*exec.Cmd, bool) {
	var scriptCommand string = s.Command
	if dryRun {
		if s.DryRun.Command == "" && s.DryRun.Variables == nil {
			return nil, true
		}
		if s.DryRun.Command != "" {
			scriptCommand = s.DryRun.Command
		}
		for _, e := range s.DryRun.Variables {
			envVars[e.Name] = e.Value
		}
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", buildBashScript(scriptCommand))
	cmd.Env = append(cmd.Env, utils.MapToEnvVarArray(envVars)...)
	return cmd, false
}

func buildBashScript(command string) string {
	return fmt.Sprintf("set -o errexit -o nounset  -o pipefail\n%s", command)
}

func (s *Step) runShellStep(ctx context.Context, kubeconfigFile string, options *PipelineRunOptions, inputs map[string]output) error {
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

	inputValues, err := getInputValues(s.Variables, inputs)
	if err != nil {
		return fmt.Errorf("failed to get input values: %w", err)
	}
	for k, v := range inputValues {
		envVars[k] = utils.AnyToString(v)
	}
	// execute the command
	cmd, skipCommand := s.createCommand(ctx, options.DryRun, envVars)
	if skipCommand {
		logger.V(5).Info(fmt.Sprintf("Skipping step '%s' due to missing dry-run configuration", s.Name))
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

func (s *Step) mapStepVariables(vars config.Variables) (map[string]string, error) {
	envVars := make(map[string]string)
	for _, e := range s.Variables {
		if e.ConfigRef != "" { // not all Variables are Environment variables
			value, found := vars.GetByPath(e.ConfigRef)
			if !found {
				return nil, fmt.Errorf("failed to lookup config reference %s for %s", e.ConfigRef, e.Name)
			}
			envVars[e.Name] = utils.AnyToString(value)
		}
	}
	return envVars, nil
}
