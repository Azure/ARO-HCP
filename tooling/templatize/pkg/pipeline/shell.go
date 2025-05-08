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
	"maps"
	"os"
	"os/exec"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/utils"
)

var OUTPUT_CAPTURE_PATH = "SHELL_EXT_OUTPUT_PATH"

func createCommand(ctx context.Context, scriptCommand string, dryRun *types.DryRun, envVars map[string]string) (*exec.Cmd, bool) {
	if dryRun != nil {
		if dryRun.Command == "" && dryRun.Variables == nil {
			return nil, true
		}
		if dryRun.Command != "" {
			scriptCommand = dryRun.Command
		}
		for _, e := range dryRun.Variables {
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

func runShellStep(s *types.ShellStep, ctx context.Context, kubeconfigFile string, options *PipelineRunOptions, inputs map[string]output) (*string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// build ENV vars
	stepVars, err := mapStepVariables(s.Variables, options.Configuration, inputs)
	if err != nil {
		return nil, fmt.Errorf("failed to build env vars: %w", err)
	}

	envVars := utils.GetOsVariable()

	maps.Copy(envVars, stepVars)

	// execute the command
	var dryRun *types.DryRun
	if options.DryRun {
		dryRun = &s.DryRun
	}
	cmd, skipCommand := createCommand(ctx, s.Command, dryRun, envVars)
	if skipCommand {
		logger.V(5).Info(fmt.Sprintf("Skipping step '%s' due to missing dry-run configuration", s.Name))
		return nil, nil
	}

	if kubeconfigFile != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", kubeconfigFile))
	}

	logger.V(5).Info(fmt.Sprintf("Executing shell command: %s\n", s.Command), "command", s.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}

	stringOutput := string(output)
	return &stringOutput, nil
}

func runShellStepAndCaptureOutput(s *types.ShellStep, ctx context.Context, kubeconfigFile string, options *PipelineRunOptions, inputs map[string]output) error {
	output, err := runShellStep(s, ctx, kubeconfigFile, options, inputs)
	if err != nil {
		return fmt.Errorf("Error running shell step %v", err)
	}
	fmt.Println(output)

	if capturePath := os.Getenv(OUTPUT_CAPTURE_PATH); capturePath != "" {
		err = os.WriteFile(capturePath, []byte(*output), 0644)
		if err != nil {
			return fmt.Errorf("Error writing shell output %v", err)
		}
	}
	return nil
}

func mapStepVariables(vars []types.Variable, cfg config.Configuration, inputs map[string]output) (map[string]string, error) {
	values, err := getInputValues(vars, cfg, inputs)
	if err != nil {
		return nil, err
	}
	envVars := make(map[string]string)
	for k, v := range values {
		envVars[k] = utils.AnyToString(v)
	}
	return envVars, nil
}
