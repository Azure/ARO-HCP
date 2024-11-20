package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func (s *step) runShellStep(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	// build ENV vars
	envVars, err := s.getEnvVars(options.Vars, true)
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	// prepare kubeconfig
	if executionTarget.AKSClusterName != "" {
		kubeconfigFile, err := executionTarget.KubeConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to build kubeconfig for %s: %w", executionTarget.aksID(), err)
		}
		defer func() {
			if err := os.Remove(kubeconfigFile); err != nil {
				fmt.Printf("Warning: failed to delete kubeconfig file %s: %v\n", kubeconfigFile, err)
			}
		}()
		envVars = append(envVars, fmt.Sprintf("KUBECONFIG=%s", kubeconfigFile))
	}

	// TODO handle dry-run

	// execute the command
	fmt.Printf("Executing shell command: %s - %s\n", s.Command[0], s.Command[1:])
	cmd := exec.CommandContext(ctx, s.Command[0], s.Command[1:]...)
	cmd.Env = append(cmd.Env, envVars...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}

	// print the output of the command
	fmt.Println(string(output))

	return nil
}

func (s *step) getEnvVars(vars config.Variables, includeOSEnvVars bool) ([]string, error) {
	envVars := make([]string, 0)
	if includeOSEnvVars {
		envVars = append(envVars, os.Environ()...)
	}
	envVars = append(envVars, "RUNS_IN_TEMPLATIZE=1")
	for _, e := range s.Env {
		value, found := vars.GetByPath(e.ConfigRef)
		if !found {
			return nil, fmt.Errorf("failed to lookup config reference %s for %s", e.ConfigRef, e.Name)
		}
		envVars = append(envVars, fmt.Sprintf("%s=%s", e.Name, value))
	}
	return envVars, nil
}
