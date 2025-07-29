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

	"github.com/google/shlex"
)

// Config represents the configuration for spawning a shell with kubeconfig access.
type Config struct {
	// KubeconfigPath is the path to the kubeconfig file to set as KUBECONFIG
	KubeconfigPath string

	// ClusterName is the display name of the cluster for prompts
	ClusterName string

	// ClusterID is the unique identifier of the cluster (used in HCP scenarios)
	ClusterID string

	// PromptInfo is the formatted prompt information to display in the shell
	// Examples: "[MC: cluster-name]" or "[cluster-id:cluster-name]"
	PromptInfo string

	// Privileged indicates whether the shell is running with privileged access
	// This affects the prompt color: blue for non-privileged, red for privileged
	Privileged bool
}

// Spawn spawns an interactive shell with KUBECONFIG environment set.
// This is a convenience function for simple use cases that handles cleanup
// coordination internally.
//
// The shell will run with the specified kubeconfig and custom prompt.
// This function blocks until the shell exits or the context is cancelled.
func Spawn(ctx context.Context, config *Config) error {
	stopCh := make(chan struct{})
	stopOnce := &sync.Once{}
	return SpawnWithCleanup(ctx, config, stopCh, stopOnce)
}

// SpawnWithCleanup spawns an interactive shell with advanced cleanup coordination.
// This function provides full control over the shell lifecycle and coordination
// with other background operations (like port forwarding).
//
// The stopCh channel is used to signal when cleanup should begin, and stopOnce
// ensures cleanup operations only happen once. This is essential for scenarios
// where multiple goroutines need to coordinate shutdown.
//
// This function blocks until the shell exits or the context is cancelled.
func SpawnWithCleanup(ctx context.Context, config *Config, stopCh chan struct{}, stopOnce *sync.Once) error {
	return spawnShell(ctx, config, stopCh, stopOnce)
}

// ExecCommandString executes a command string directly with KUBECONFIG environment set.
// This is a convenience function that parses a command string and calls ExecCommand.
//
// The command string is parsed using shell-like parsing with support for quoted arguments.
//
// This function blocks until the command exits or the context is cancelled.
func ExecCommandString(ctx context.Context, config *Config, commandStr string, stopCh chan struct{}, stopOnce *sync.Once) error {
	command, err := shlex.Split(commandStr)
	if err != nil {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("failed to parse command: %w", err)
	}

	if len(command) == 0 {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("empty command provided")
	}
	return ExecCommand(ctx, config, command, stopCh, stopOnce)
}

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
	shell, err := detectShell()
	if err != nil {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("failed to detect shell: %w", err)
	}

	// Create shell command with environment
	cmd := shell.CreateCommand(ctx, config, kubeconfigPath)

	// Start the shell
	if err := cmd.Start(); err != nil {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("failed to start %s: %w", shell.Name(), err)
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

// execCommand is the internal implementation that executes a command directly
// with KUBECONFIG environment set, following the same patterns as spawnShell.
func ExecCommand(ctx context.Context, config *Config, command []string, stopCh chan struct{}, stopOnce *sync.Once) error {
	// Get absolute path for kubeconfig
	kubeconfigPath, err := filepath.Abs(config.KubeconfigPath)
	if err != nil {
		// fallback to relative path if absolute path fails
		kubeconfigPath = config.KubeconfigPath
	}

	if len(command) == 0 {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("no command specified for execution")
	}

	// Create command with environment
	cmd := createExecCommand(ctx, config, command, kubeconfigPath)

	// Start the command
	if err := cmd.Start(); err != nil {
		stopOnce.Do(func() { close(stopCh) })
		return fmt.Errorf("failed to start command %v: %w", command, err)
	}

	// Wait for command to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Wait for command exit or context cancellation
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

// createExecCommand creates an exec.Cmd for direct command execution with KUBECONFIG environment set
func createExecCommand(ctx context.Context, config *Config, command []string, kubeconfigPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	// setup env with KUBECONFIG
	cmd.Env = setEnvironmentVariable(os.Environ(), "KUBECONFIG", kubeconfigPath)

	// Connect I/O
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}

// setEnvironmentVariable sets or overrides a specific environment variable in the given environment slice.
// If the variable already exists, it updates its value. Otherwise, it appends the new variable.
func setEnvironmentVariable(env []string, key, value string) []string {
	prefix := key + "="
	newVar := prefix + value

	for i, envVar := range env {
		if strings.HasPrefix(envVar, prefix) {
			env[i] = newVar
			return env
		}
	}

	return append(env, newVar)
}
