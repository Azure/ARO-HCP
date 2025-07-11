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
		shell, err := detectShell()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if shell == nil {
			t.Error("Expected shell, got nil")
		} else if shell.Name() != "zsh" {
			t.Errorf("Expected zsh, got %s", shell.Name())
		}
	})

	t.Run("unix without SHELL env var", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping Unix test on Windows")
		}

		t.Setenv("SHELL", "")
		shell, err := detectShell()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if shell == nil {
			t.Error("Expected shell, got nil")
		} else if shell.Name() != "bash" {
			t.Errorf("Expected bash fallback, got %s", shell.Name())
		}
	})

	t.Run("windows shell detection", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Skipping Windows test on non-Windows")
		}

		shell, err := detectShell()

		// On Windows, might get an error if no PowerShell is found
		if err != nil {
			if !strings.Contains(err.Error(), "PowerShell not found") {
				t.Errorf("Unexpected error: %v", err)
			}
		} else if shell != nil {
			// Should be PowerShell implementation
			name := shell.Name()
			if name != "PowerShell Core" && name != "Windows PowerShell" {
				t.Errorf("Expected PowerShell implementation, got %s", name)
			}
		}
	})

	t.Run("cross-platform compatibility", func(t *testing.T) {
		// This test should pass on any platform
		shell, err := detectShell()

		if runtime.GOOS != "windows" {
			// Unix systems should always succeed
			if err != nil {
				t.Errorf("Unix shell detection should not error: %v", err)
			}
			if shell == nil {
				t.Error("Unix shell detection should never return nil")
			}
		}
		// Windows might error if PowerShell is not available
	})
}

func TestDetectShellFallback(t *testing.T) {
	t.Run("returns valid shell or error", func(t *testing.T) {
		shell, err := detectShell()

		if runtime.GOOS == "windows" {
			// Windows might error if no PowerShell available
			if err == nil && shell == nil {
				t.Error("Should not return nil shell without error")
			}
		} else {
			// Unix should always succeed
			if err != nil {
				t.Errorf("Unix should not error: %v", err)
			}
			if shell == nil {
				t.Error("Unix should never return nil shell")
			}
		}
	})
}

func TestSetEnvironmentVariable(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		key      string
		value    string
		expected []string
	}{
		{
			name:     "add new variable",
			env:      []string{"HOME=/home/user", "PATH=/usr/bin"},
			key:      "KUBECONFIG",
			value:    "/test/kubeconfig",
			expected: []string{"HOME=/home/user", "PATH=/usr/bin", "KUBECONFIG=/test/kubeconfig"},
		},
		{
			name:     "replace existing variable",
			env:      []string{"HOME=/home/user", "KUBECONFIG=/old/path", "PATH=/usr/bin"},
			key:      "KUBECONFIG",
			value:    "/new/path",
			expected: []string{"HOME=/home/user", "KUBECONFIG=/new/path", "PATH=/usr/bin"},
		},
		{
			name:     "empty environment",
			env:      []string{},
			key:      "KUBECONFIG",
			value:    "/test/kubeconfig",
			expected: []string{"KUBECONFIG=/test/kubeconfig"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := setEnvironmentVariable(tt.env, tt.key, tt.value)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d env vars, got %d", len(tt.expected), len(result))
			}

			// Check that the expected variable is set correctly
			found := false
			expectedVar := tt.key + "=" + tt.value
			for _, envVar := range result {
				if envVar == expectedVar {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected %s to be set in environment", expectedVar)
			}
		})
	}
}

func TestPowerShellName(t *testing.T) {
	testCases := []struct {
		name       string
		executable string
		expected   string
	}{
		{
			name:       "PowerShell Core with pwsh.exe",
			executable: "pwsh.exe",
			expected:   "PowerShell Core",
		},
		{
			name:       "Windows PowerShell with powershell.exe",
			executable: "powershell.exe",
			expected:   "Windows PowerShell",
		},
		{
			name:       "Other PowerShell executables",
			executable: "some-other-powershell.exe",
			expected:   "Windows PowerShell",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ps := NewPowerShell(tc.executable)
			result := ps.Name()
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestUnixShellName(t *testing.T) {
	testCases := []struct {
		name       string
		executable string
		expected   string
	}{
		{
			name:       "returns basename of executable for full path",
			executable: "/bin/bash",
			expected:   "bash",
		},
		{
			name:       "returns basename for zsh",
			executable: "/usr/bin/zsh",
			expected:   "zsh",
		},
		{
			name:       "returns basename for sh",
			executable: "/bin/sh",
			expected:   "sh",
		},
		{
			name:       "returns executable name when no path",
			executable: "bash",
			expected:   "bash",
		},
		{
			name:       "handles complex paths",
			executable: "/usr/local/bin/fish",
			expected:   "fish",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shell := NewUnixShell(tc.executable)
			result := shell.Name()
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}
