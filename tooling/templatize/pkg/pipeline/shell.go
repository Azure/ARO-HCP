package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func (s *step) runShellStep(ctx context.Context, executionTarget *StepExecutionTarget, vars config.Variables) error {
	fmt.Printf("Execution target: %s\n", executionTarget.Name())
	fmt.Printf("Shell command: %v\n", s.Action.Command)

	// prepare kubeconfig
	kubeconfigFile, err := executionTarget.KubeConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	// schedule the deletion of the kubeconfig file after the command execution
	defer func() {
		if err := os.Remove(kubeconfigFile); err != nil {
			fmt.Printf("Warning: failed to delete kubeconfig file %s: %v\n", kubeconfigFile, err)
		}
	}()

	// build ENV vars
	envVars := os.Environ()
	if executionTarget.AKSClusterName != "" {
		kubeconfigFile, err := executionTarget.KubeConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to build kubeconfig for %s: %w", executionTarget.Name(), err)
		}
		envVars = append(envVars, fmt.Sprintf("KUBECONFIG=%s", kubeconfigFile))
	}
	for _, e := range s.Action.Env {
		value := vars[e.ConfigRef] // todo nested lookups
		envVars = append(envVars, fmt.Sprintf("%s=%s", e.Name, value))
	}

	// execute the shell command with the environment variables
	cmd := exec.CommandContext(ctx, s.Action.Command[0], s.Action.Command[1:]...)
	cmd.Env = append(cmd.Env, envVars...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}

	// print the output of the command
	fmt.Println(string(output))

	return nil
}
