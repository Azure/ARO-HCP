package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/utils"
)

func (s *step) runShellStep(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	logger := logr.FromContextOrDiscard(ctx)

	// build ENV vars
	envVars, err := s.getEnvVars(options.Vars, true)
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	// prepare kubeconfig
	if executionTarget.AKSClusterName != "" {
		logger.V(5).Info("Building kubeconfig for AKS cluster")
		kubeconfigFile, err := executionTarget.KubeConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to build kubeconfig for %s: %w", executionTarget.aksID(), err)
		}
		defer func() {
			if err := os.Remove(kubeconfigFile); err != nil {
				logger.V(5).Error(err, "failed to delete kubeconfig file", "kubeconfig", kubeconfigFile)
			}
		}()
		envVars["KUBECONFIG"] = kubeconfigFile
		logger.V(5).Info("kubeconfig set to shell execution environment", "kubeconfig", kubeconfigFile)
	}

	// TODO handle dry-run

	// execute the command
	logger.V(5).Info(fmt.Sprintf("Executing shell command: %s\n", s.Command), "command", s.Command)
	cmd := exec.CommandContext(ctx, s.Command[0], s.Command[1:]...)
	cmd.Env = append(cmd.Env, utils.MapToEnvVarArray(envVars)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}

	// print the output of the command
	fmt.Println(string(output))

	return nil
}

func (s *step) getEnvVars(vars config.Variables, includeOSEnvVars bool) (map[string]string, error) {
	envVars := make(map[string]string)
	envVars["RUNS_IN_TEMPLATIZE"] = "1"
	if includeOSEnvVars {
		for k, v := range utils.GetOSEnvVarsAsMap() {
			envVars[k] = v
		}
	}
	for _, e := range s.Env {
		value, found := vars.GetByPath(e.ConfigRef)
		if !found {
			return nil, fmt.Errorf("failed to lookup config reference %s for %s", e.ConfigRef, e.Name)
		}
		envVars[e.Name] = anyToString(value)
	}
	return envVars, nil
}

func anyToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
