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
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	"github.com/Azure/ARO-Tools/pkg/registration"
	"github.com/Azure/ARO-Tools/pkg/secret-sync/populate"
	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/utils"
)

func createCommand(ctx context.Context, scriptCommand, pipelineWorkingDir string, dryRun *types.DryRun, envVars map[string]string) (*exec.Cmd, bool) {
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
	cmd.Dir = pipelineWorkingDir
	return cmd, false
}

func buildBashScript(command string) string {
	return fmt.Sprintf("set -o errexit -o nounset  -o pipefail\n%s", command)
}

func runShellStep(s *types.ShellStep, ctx context.Context, kubeconfigFile string, options *StepRunOptions, state *ExecutionState, outputWriter io.Writer) error {
	logger := logr.FromContextOrDiscard(ctx)

	// set dryRun config if needed
	var dryRun *types.DryRun
	var dryRunVars map[string]string
	var err error
	if options.DryRun {
		dryRun = &s.DryRun
		state.RLock()
		dryRunVars, err = mapStepVariables(dryRun.Variables, options.Configuration, state.Outputs)
		state.RUnlock()
		if err != nil {
			return fmt.Errorf("failed to build dry run vars: %w", err)
		}
	}

	// build ENV vars
	state.RLock()
	stepVars, err := mapStepVariables(s.Variables, options.Configuration, state.Outputs)
	state.RUnlock()
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	envVars := utils.GetOsVariable()

	maps.Copy(envVars, stepVars)
	maps.Copy(envVars, dryRunVars)

	workingDir := options.PipelineDirectory
	if s.WorkingDir != "" {
		workingDir = filepath.Join(options.PipelineDirectory, s.WorkingDir)
	}
	cmd, skipCommand := createCommand(ctx, s.Command, workingDir, dryRun, envVars)
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

func runSecretSyncStep(s *types.SecretSyncStep, ctx context.Context, options *StepRunOptions) error {
	logger := logr.FromContextOrDiscard(ctx)
	if options.DryRun {
		logger.Info("Skipping secret sync step for dry-run.")
		return nil
	}
	syncOpts := populate.RawOptions{
		RawOptions: &cmdutils.RawOptions{
			Cloud: options.Cloud,
		},
		KeyVault:         s.KeyVault,
		KeyEncryptionKey: s.EncryptionKey,
		ConfigFile:       filepath.Join(options.PipelineDirectory, s.ConfigurationFile),
	}
	validated, err := syncOpts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.Populate(ctx)
}

func runRegistrationStep(s *types.ProviderFeatureRegistrationStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget) error {
	logger := logr.FromContextOrDiscard(ctx)
	if options.DryRun {
		logger.Info("Skipping provider and feature registration step for dry-run.")
		return nil
	}

	rawCfg, err := options.Configuration.GetByPath(s.ProviderConfigRef)
	if err != nil {
		return fmt.Errorf("failed to get raw registration configuration: %w", err)
	}

	encoded, err := json.Marshal(rawCfg)
	if err != nil {
		return fmt.Errorf("failed to serialize raw registration configuration: %w", err)
	}

	registrationOpts := registration.RawOptions{
		RawOptions: &cmdutils.RawOptions{
			Cloud: options.Cloud,
		},
		ConfigJSON:     string(encoded),
		SubscriptionID: executionTarget.GetSubscriptionID(),
		PollFrequency:  10 * time.Second,
		PollDuration:   10 * time.Minute,
	}
	validated, err := registrationOpts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.Register(ctx)
}

func mapStepVariables(vars []types.Variable, cfg config.Configuration, inputs Outputs) (map[string]string, error) {
	values, err := getInputValues(vars, cfg, inputs)
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	envVars := make(map[string]string)
	for k, v := range values {
		envVars[k] = utils.AnyToString(v)
	}
	return envVars, nil
}
