package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func (s *step) runShellStep(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	// build ENV vars
	envVars := os.Environ()
	if executionTarget.AKSClusterName != "" {
		// prepare kubeconfig
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

	// prepare declared env vars
	for _, e := range s.Env {
		value, found := options.Vars.GetByPath(e.ConfigRef)
		if !found {
			return fmt.Errorf("failed to lookup config reference %s for %s", e.ConfigRef, e.Name)
		}
		envVars = append(envVars, fmt.Sprintf("%s=%s", e.Name, value))
	}

	// TODO handle dry-run

	// execute the command
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
