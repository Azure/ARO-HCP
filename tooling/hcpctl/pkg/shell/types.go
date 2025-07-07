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
	"strings"
)

// Shell is the interface that all shell implementations must satisfy.
// It provides methods for creating and configuring shell commands
// with kubeconfig access.
type Shell interface {
	// Name returns the display name of the shell type
	Name() string

	// CreateCommand creates the exec.Cmd for the shell with appropriate configuration
	// including environment setup and I/O redirection
	CreateCommand(ctx context.Context, config *Config, kubeconfigPath string) *exec.Cmd
}

type PowerShell struct {
	executable string
}

func NewPowerShell(executable string) *PowerShell {
	return &PowerShell{executable: executable}
}

func (p *PowerShell) Name() string {
	if strings.Contains(strings.ToLower(p.executable), "pwsh") {
		return "PowerShell Core"
	}
	return "Windows PowerShell"
}

func (p *PowerShell) CreateCommand(ctx context.Context, config *Config, kubeconfigPath string) *exec.Cmd {
	promptColor := "Blue"
	if config.Privileged {
		promptColor = "Red"
	}
	startupScript := fmt.Sprintf(`
function prompt {
    Write-Host "%s " -ForegroundColor %s -NoNewline
    "PS $($executionContext.SessionState.Path.CurrentLocation)> "
}
`, config.PromptInfo, promptColor)
	cmd := exec.CommandContext(ctx, p.executable, "-NoLogo", "-NoExit", "-Command", startupScript)

	// setup env
	env := cmd.Environ()
	env = setEnvironmentVariable(env, "KUBECONFIG", kubeconfigPath)

	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}

type UnixShell struct {
	executable string
}

func NewUnixShell(executable string) *UnixShell {
	return &UnixShell{executable: executable}
}

func (u *UnixShell) Name() string {
	parts := strings.Split(u.executable, "/")
	return parts[len(parts)-1]
}

func (u *UnixShell) CreateCommand(ctx context.Context, config *Config, kubeconfigPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, u.executable)

	// setup env
	env := cmd.Environ()
	env = setEnvironmentVariable(env, "KUBECONFIG", kubeconfigPath)

	// set custom prompt with color based on privilege level
	var promptColor string
	if config.Privileged {
		promptColor = "\\[\\033[1;31m\\]" // red
	} else {
		promptColor = "\\[\\033[1;34m\\]" // blue
	}
	prompt := fmt.Sprintf("%s%s\\[\\033[0m\\] \\u@\\h:\\w$ ", promptColor, config.PromptInfo)
	env = setEnvironmentVariable(env, "PS1", prompt)

	// Suppress macOS bash deprecation warning that appears when bash is spawned
	// on systems where zsh is the default shell. This prevents the noisy message:
	// "The default interactive shell is now zsh. To update your account to use zsh..."
	env = setEnvironmentVariable(env, "BASH_SILENCE_DEPRECATION_WARNING", "1")

	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}
