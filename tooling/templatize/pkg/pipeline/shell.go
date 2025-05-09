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
	"io"
	"maps"
	"os/exec"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/utils"
)

func createCommand(ctx context.Context, scriptCommand string, dryRun *types.DryRun, envVars map[string]string) (*exec.Cmd, bool) {
	if dryRun != nil {
		if dryRun.Command == "" && dryRun.Variables == nil {
			return nil, true
		}
		if dryRun.Command != "" {
			scriptCommand = dryRun.Command
		}
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", buildBashScript(scriptCommand))
	cmd.Env = append(cmd.Env, utils.MapToEnvVarArray(envVars)...)
	return cmd, false
}

func buildBashScript(command string) string {
	return fmt.Sprintf("set -o errexit -o nounset  -o pipefail\n%s", command)
}

func runShellStep(s *types.ShellStep, ctx context.Context, kubeconfigFile string, options *PipelineRunOptions, inputs map[string]Output, outputWriter io.Writer) error {
	logger := logr.FromContextOrDiscard(ctx)

	// set dryRun config if needed
	var dryRun *types.DryRun
	var dryRunVars map[string]string
	var err error
	if options.DryRun {
		dryRun = &s.DryRun
		dryRunVars, err = mapStepVariables(dryRun.Variables, options.Configuration, inputs)
		if err != nil {
			return fmt.Errorf("failed to build dry run vars: %w", err)
		}
	}

	// build ENV vars
	stepVars, err := mapStepVariables(s.Variables, options.Configuration, inputs)
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	envVars := utils.GetOsVariable()

	maps.Copy(envVars, stepVars)
	maps.Copy(envVars, dryRunVars)

	cmd, skipCommand := createCommand(ctx, s.Command, dryRun, envVars)
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

	fmt.Fprint(outputWriter, string(output))
	return nil
}

func mapStepVariables(vars []types.Variable, cfg config.Configuration, inputs map[string]Output) (map[string]string, error) {
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
