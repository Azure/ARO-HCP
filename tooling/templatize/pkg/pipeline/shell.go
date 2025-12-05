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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/registration"
	"github.com/Azure/ARO-Tools/pkg/secret-sync/populate"
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

func runShellStep(id graph.Identifier, s *types.ShellStep, ctx context.Context, kubeconfigFile string, options *StepRunOptions, state *ExecutionState, outputWriter io.Writer) error {
	logger := logr.FromContextOrDiscard(ctx)

	// set dryRun config if needed
	var dryRun *types.DryRun
	var dryRunVars map[string]string
	var err error
	if options.DryRun {
		dryRun = &s.DryRun
		state.RLock()
		dryRunVars, err = mapStepVariables(id.ServiceGroup, dryRun.Variables, options.Configuration, state.Outputs)
		state.RUnlock()
		if err != nil {
			return fmt.Errorf("failed to build dry run vars: %w", err)
		}
	}

	// build ENV vars
	state.RLock()
	stepVars, err := mapStepVariables(id.ServiceGroup, s.Variables, options.Configuration, state.Outputs)
	state.RUnlock()
	if err != nil {
		return fmt.Errorf("failed to build env vars: %w", err)
	}

	envVars := utils.GetOsVariable()

	maps.Copy(envVars, stepVars)
	maps.Copy(envVars, dryRunVars)

	if kubeconfigFile != "" {
		envVars["KUBECONFIG"] = kubeconfigFile // TODO: we need to put the kubeconfig in a deterministic place so we can omit re-runs
	}

	workingDir := options.PipelineDirectory
	if s.WorkingDir != "" {
		workingDir = filepath.Join(options.PipelineDirectory, s.WorkingDir)
	}

	commit := func() error {
		return nil
	}
	if s.IsWellFormedOverInputs() {
		skip, commitFunc, err := checkSentinel(logger, map[string]any{
			"command":    s.Command,
			"workingDir": workingDir,
			"dryRun":     dryRun,
			"envVars":    envVars,
		}, options.StepCacheDir)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
		commit = commitFunc
	}

	configureAzureCLILogin(ctx)

	cmd, skipCommand := createCommand(ctx, s.Command, workingDir, dryRun, envVars)
	if skipCommand {
		logger.V(5).Info(fmt.Sprintf("Skipping step '%s' due to missing dry-run configuration", s.Name))
		return nil
	}

	logger.V(5).Info(fmt.Sprintf("Executing shell command: %s\n", s.Command), "command", s.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shell command: %s %w", string(output), err)
	}
	logger.V(4).Info(fmt.Sprintf("Output from shell command: %s\n", string(output)))

	fmt.Fprint(outputWriter, string(output))
	return commit()
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
	rawConfig, err := os.ReadFile(syncOpts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to read secret sync config: %w", err)
	}

	skip, commit, err := checkSentinel(logger, map[string]any{
		"opts":   syncOpts,
		"config": rawConfig,
	}, options.StepCacheDir)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	validated, err := syncOpts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	if err := completed.Populate(ctx); err != nil {
		return err
	}
	return commit()
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

	skip, commit, err := checkSentinel(logger, registrationOpts, options.StepCacheDir)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	validated, err := registrationOpts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	if err := completed.Register(ctx); err != nil {
		return err
	}

	return commit()
}

var successSentinel = []byte(`success`)

func checkSentinel(logger logr.Logger, data any, stepCacheDir string) (bool, func() error, error) {
	if stepCacheDir == "" {
		logger.V(4).Info("No cache directory provided, omitting step execution cache.")
		return false, func() error {
			return nil
		}, nil
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return false, nil, fmt.Errorf("failed to serialize registration options: %w", err)
	}
	hash := sha256.New()
	hash.Write(encoded)
	hashBytes := hash.Sum(nil)
	digest := hex.EncodeToString(hashBytes)
	logger = logger.WithValues("digest", digest)
	logger.V(4).Info("Calculated step input digest.")
	logger.V(8).Info("Divulging step inputs.", "inputs", string(encoded))

	if content, err := os.ReadFile(filepath.Join(stepCacheDir, digest)); err == nil && bytes.Equal(content, successSentinel) {
		logger.Info("Found cached successful run, returning.")
		return true, nil, nil
	} else {
		logger.V(4).Info("Did not find any content in cache.", "err", err)
	}
	return false, func() error {
		logger.Info("Committing success for step to cache.")
		return os.WriteFile(filepath.Join(stepCacheDir, digest), successSentinel, 0644)
	}, nil
}

func checkCachedOutput[T any](logger logr.Logger, data any, stepCacheDir string) (string, *T, func(T) error, error) {
	if stepCacheDir == "" {
		logger.V(4).Info("No cache directory provided, omitting step execution cache.")
		return "", nil, func(T) error {
			return nil
		}, nil
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to serialize registration options: %w", err)
	}
	hash := sha256.New()
	hash.Write(encoded)
	hashBytes := hash.Sum(nil)
	digest := hex.EncodeToString(hashBytes)
	logger = logger.WithValues("digest", digest)
	logger.V(4).Info("Calculated step input digest.")
	logger.V(8).Info("Divulging step inputs.", "inputs", string(encoded))

	if content, err := os.ReadFile(filepath.Join(stepCacheDir, digest)); err == nil {
		logger.Info("Found cached successful run, returning.")

		var output T
		if err := json.Unmarshal(content, &output); err != nil {
			return "", nil, nil, fmt.Errorf("failed to deserialize output: %w", err)
		}
		return digest, &output, nil, nil
	} else {
		logger.V(4).Info("Did not find any content in cache.", "err", err)
	}
	return digest, nil, func(output T) error {
		logger.Info("Committing success for step.")
		encoded, err := json.Marshal(output)
		if err != nil {
			return fmt.Errorf("failed to serialize output: %w", err)
		}
		return os.WriteFile(filepath.Join(stepCacheDir, digest), encoded, 0644)
	}, nil
}

func mapStepVariables(serviceGroup string, vars []types.Variable, cfg configtypes.Configuration, inputs Outputs) (map[string]string, error) {
	values, err := getInputValues(serviceGroup, vars, cfg, inputs)
	if err != nil {
		return nil, fmt.Errorf("failed to get input values: %w", err)
	}
	envVars := make(map[string]string)
	for k, v := range values {
		envVars[k] = utils.AnyToString(v)
	}
	return envVars, nil
}

// getAzureConfigDir gets the Azure CLI config directory by running `az config get --query 'cloud[0].source' -o tsv`
// and extracting the directory from the source path in the output
func getAzureConfigDir(ctx context.Context, logger logr.Logger) (string, error) {
	cmd := exec.CommandContext(ctx, "az", "config", "get", "--query", "cloud[0].source", "-o", "tsv")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run az config get: %w", err)
	}

	// Trim whitespace and extract directory from the config file path
	configFile := string(bytes.TrimSpace(output))
	if configFile == "" {
		return "", fmt.Errorf("no source path found in az config get output")
	}

	configDir := filepath.Dir(configFile)
	logger.V(4).Info("Found Azure CLI config directory from az config get", "dir", configDir)
	return configDir, nil
}

func configureAzureCLILogin(ctx context.Context, subscriptionID string) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Create a temporary directory for the Azure CLI login
	tmpDir, err := os.MkdirTemp("", "azure-cli-config-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}

	// Get azure cli config directory using az config get
	azureConfigDir, err := getAzureConfigDir(ctx, logger)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure CLI config directory: %w", err)
	}

	// Copy the config directory to the temporary directory
	if err := copyDirectory(azureConfigDir, tmpDir); err != nil {
		return "", fmt.Errorf("failed to copy Azure CLI config directory: %w", err)
	}

	// Set the AZURE_CONFIG_DIR environment variable to the temporary directory
	// and run az config get to ensure the config is set correctly
	env := os.Environ()
	env = append(env, fmt.Sprintf("AZURE_CONFIG_DIR=%s", tmpDir))
	cmd := exec.CommandContext(ctx, "az", "config", "get")
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		logger.V(4).Info("az config get failed (may be expected if config is empty)", "output", string(output), "err", err)
		// Don't fail here, as empty config is valid
	}

	// Run az account set --subscription $subscriptionID
	cmd = exec.CommandContext(ctx, "az", "account", "set", "--subscription", subscriptionID)
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to set Azure subscription: %s %w", string(output), err)
	}
	// Return the temporary directory
	return tmpDir, nil
}

// copyDirectory recursively copies a directory from src to dst
func copyDirectory(src, dst string) error {
	return os.CopyFS(dst, os.DirFS(src))
}
