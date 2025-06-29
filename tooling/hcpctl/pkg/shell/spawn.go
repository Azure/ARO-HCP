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

package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// spawnShell is the internal implementation that spawns an interactive shell
// with KUBECONFIG environment set and custom prompt configuration.
//
// This implementation is based on the robust async pattern from the HCP
// implementation, providing proper context cancellation and cleanup coordination.
func spawnShell(ctx context.Context, config *Config, stopCh chan struct{}, stopOnce *sync.Once) error {
	// Get absolute path for kubeconfig
	kubeconfigPath, err := filepath.Abs(config.KubeconfigPath)
	if err != nil {
		// fallback to relative path if absolute path fails
		kubeconfigPath = config.KubeconfigPath
	}

	// Detect appropriate shell
	shell := detectShell()

	// Prepare environment with KUBECONFIG set
	env := setupEnvironment(kubeconfigPath)

	// Create shell command with appropriate initialization
	cmd := createShellCommand(ctx, shell, config, env)

	// Start the shell
	if err := cmd.Start(); err != nil {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Wait for shell to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Wait for shell exit or context cancellation
	select {
	case err := <-done:
		stopOnce.Do(func() { close(stopCh) })
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		stopOnce.Do(func() { close(stopCh) })
		return ctx.Err()
	}
}

// setupEnvironment creates the environment variables for the shell process.
// It sets KUBECONFIG and adds kubectl noise suppression variables.
func setupEnvironment(kubeconfigPath string) []string {
	env := os.Environ()

	// Set KUBECONFIG, replacing any existing value
	kubeconfigSet := false
	for i, envVar := range env {
		if strings.HasPrefix(envVar, "KUBECONFIG=") {
			env[i] = "KUBECONFIG=" + kubeconfigPath
			kubeconfigSet = true
			break
		}
	}
	if !kubeconfigSet {
		env = append(env, "KUBECONFIG="+kubeconfigPath)
	}

	// Add kubectl noise suppression to reduce verbose output
	env = append(env,
		"KLOG_V=0",                   // Set klog verbosity to 0
		"KLOG_STDERRTHRESHOLD=4",     // Only log above FATAL errors
		"KLOG_LOGTOSTDERR=false",     // Don't log to stderr
		"KLOG_ALSOLOGTOSTDERR=false", // Don't also log to stderr
		"KUBECTL_LOG_LEVEL=0",        // Set kubectl log level to 0
		"KUBECTL_SUPPRESS_NOISE=1",   // Suppress kubectl noise
		"KUBECTL_EXTERNAL_DIFF=",     // Disable external diff to reduce noise
	)

	return env
}

// createShellCommand creates the appropriate shell command based on the detected shell type.
// It handles shell-specific prompt configuration and startup scripts.
func createShellCommand(ctx context.Context, shell string, config *Config, env []string) *exec.Cmd {
	var cmd *exec.Cmd

	shellLower := strings.ToLower(shell)

	if strings.Contains(shellLower, "pwsh") || strings.Contains(shellLower, "powershell") {
		// PowerShell configuration with custom prompt
		startupScript := createPowerShellStartup(config)
		cmd = exec.CommandContext(ctx, shell, "-NoLogo", "-NoExit", "-Command", startupScript)
	} else {
		// Bash/sh configuration with custom prompt
		env = setupBashPrompt(env, config)
		cmd = exec.CommandContext(ctx, shell)

		// Print welcome message for Unix shells (PowerShell handles this in startup script)
		printWelcomeMessage(config)
	}

	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}

// createPowerShellStartup creates a PowerShell startup script with custom prompt and welcome message.
func createPowerShellStartup(config *Config) string {
	return fmt.Sprintf(`
function prompt {
    Write-Host "%s " -ForegroundColor Green -NoNewline
    "PS $($executionContext.SessionState.Path.CurrentLocation)> "
}
Write-Host ""
Write-Host "Breakglass shell activated for: %s" -ForegroundColor Yellow
Write-Host "KUBECONFIG set to: $env:KUBECONFIG" -ForegroundColor Gray
Write-Host ""
`, config.PromptInfo, getDisplayName(config))
}

// setupBashPrompt configures the bash prompt with cluster information.
func setupBashPrompt(env []string, config *Config) []string {
	// Create colored bash prompt with cluster info
	prompt := fmt.Sprintf("\\[\\033[1;32m\\]%s\\[\\033[0m\\] \\u@\\h:\\w$ ", config.PromptInfo)
	return append(env, "PS1="+prompt)
}

// printWelcomeMessage prints a welcome message for Unix shells.
func printWelcomeMessage(config *Config) {
	fmt.Printf("Breakglass shell activated for: %s\n", getDisplayName(config))
	fmt.Printf("KUBECONFIG set to: %s\n\n", config.KubeconfigPath)
}

// getDisplayName returns an appropriate display name for the cluster.
func getDisplayName(config *Config) string {
	if config.ClusterID != "" && config.ClusterName != "" {
		return fmt.Sprintf("%s (%s)", config.ClusterName, config.ClusterID)
	}
	if config.ClusterName != "" {
		return config.ClusterName
	}
	if config.ClusterID != "" {
		return config.ClusterID
	}
	return "unknown cluster"
}
