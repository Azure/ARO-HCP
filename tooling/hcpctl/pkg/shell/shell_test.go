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
	"runtime"
	"strings"
	"testing"
)

func TestDetectShell(t *testing.T) {
	t.Run("unix with SHELL env var", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping Unix test on Windows")
		}

		t.Setenv("SHELL", "/bin/zsh")
		shell := detectShell()
		if shell != "/bin/zsh" {
			t.Errorf("Expected /bin/zsh, got %s", shell)
		}
	})

	t.Run("unix without SHELL env var", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping Unix test on Windows")
		}

		t.Setenv("SHELL", "")
		shell := detectShell()
		if shell != "/bin/bash" {
			t.Errorf("Expected /bin/bash fallback, got %s", shell)
		}
	})

	t.Run("windows shell detection", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Skipping Windows test on non-Windows")
		}

		shell := detectShell()

		// On Windows, should be one of the expected shells
		validShells := []string{"pwsh.exe", "powershell.exe", "cmd.exe"}
		isValid := false
		for _, validShell := range validShells {
			if strings.Contains(shell, validShell) || shell == validShell {
				isValid = true
				break
			}
		}

		if !isValid {
			t.Errorf("Expected Windows shell (pwsh.exe, powershell.exe, or cmd.exe), got %s", shell)
		}
	})

	t.Run("cross-platform compatibility", func(t *testing.T) {
		// This test should pass on any platform
		shell := detectShell()

		if shell == "" {
			t.Error("detectShell should never return empty string")
		}

		// Verify the shell looks reasonable for the platform
		switch runtime.GOOS {
		case "windows":
			if !strings.Contains(shell, ".exe") {
				t.Errorf("Windows shell should end with .exe, got %s", shell)
			}
		default:
			if strings.Contains(shell, ".exe") {
				t.Errorf("Unix shell should not end with .exe, got %s", shell)
			}
		}
	})
}

func TestDetectShellFallback(t *testing.T) {
	t.Run("always returns a valid shell", func(t *testing.T) {
		// Even in worst case scenario, should return a fallback
		shell := detectShell()

		if shell == "" {
			t.Error("Shell detection should never return empty string")
		}

		// Should not contain obviously invalid patterns
		invalidPatterns := []string{
			"/bin/bash.exe", // Mixed Unix/Windows
			"cmd",           // Should be cmd.exe on Windows
			"/pwsh.exe",     // Mixed path styles
		}

		for _, pattern := range invalidPatterns {
			if shell == pattern {
				t.Errorf("Shell should not be %s (invalid pattern)", pattern)
			}
		}
	})
}

func TestGetDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected string
	}{
		{
			name: "both cluster ID and name",
			config: &Config{
				ClusterID:   "abc123",
				ClusterName: "my-cluster",
			},
			expected: "my-cluster (abc123)",
		},
		{
			name: "only cluster name",
			config: &Config{
				ClusterName: "my-cluster",
			},
			expected: "my-cluster",
		},
		{
			name: "only cluster ID",
			config: &Config{
				ClusterID: "abc123",
			},
			expected: "abc123",
		},
		{
			name:     "empty config",
			config:   &Config{},
			expected: "unknown cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getDisplayName(tt.config)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestSetupEnvironment(t *testing.T) {
	kubeconfigPath := "/test/kubeconfig"
	env := setupEnvironment(kubeconfigPath)

	// Check that KUBECONFIG is set
	found := false
	for _, envVar := range env {
		if envVar == "KUBECONFIG="+kubeconfigPath {
			found = true
			break
		}
	}
	if !found {
		t.Error("KUBECONFIG environment variable not set correctly")
	}

	// Check that kubectl noise suppression variables are present
	expectedVars := []string{
		"KLOG_V=0",
		"KLOG_STDERRTHRESHOLD=4",
		"KUBECTL_LOG_LEVEL=0",
		"KUBECTL_SUPPRESS_NOISE=1",
	}

	for _, expectedVar := range expectedVars {
		found := false
		for _, envVar := range env {
			if envVar == expectedVar {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected environment variable %s not found", expectedVar)
		}
	}
}

func TestCreatePowerShellStartup(t *testing.T) {
	config := &Config{
		ClusterName: "test-cluster",
		ClusterID:   "abc123",
		PromptInfo:  "[abc123:test-cluster]",
	}

	startup := createPowerShellStartup(config)

	// Check that prompt info is included
	if !strings.Contains(startup, config.PromptInfo) {
		t.Error("PowerShell startup script should contain prompt info")
	}

	// Check that cluster name is included
	if !strings.Contains(startup, config.ClusterName) {
		t.Error("PowerShell startup script should contain cluster name")
	}

	// Check that it contains prompt function
	if !strings.Contains(startup, "function prompt") {
		t.Error("PowerShell startup script should contain prompt function")
	}
}

func TestSetupBashPrompt(t *testing.T) {
	config := &Config{
		PromptInfo: "[MC: test-cluster]",
	}

	env := []string{"HOME=/home/user"}
	result := setupBashPrompt(env, config)

	// Should have added PS1 variable
	found := false
	for _, envVar := range result {
		if strings.HasPrefix(envVar, "PS1=") && strings.Contains(envVar, config.PromptInfo) {
			found = true
			break
		}
	}

	if !found {
		t.Error("Bash prompt setup should add PS1 variable with prompt info")
	}

	// Should preserve existing environment
	if len(result) != len(env)+1 {
		t.Error("Bash prompt setup should preserve existing environment variables")
	}
}
