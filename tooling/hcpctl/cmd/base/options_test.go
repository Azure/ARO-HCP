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

package base

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestDefaultBaseOptions tests the default base options creation.
func TestDefaultBaseOptions(t *testing.T) {
	t.Run("with KUBECONFIG env var", func(t *testing.T) {
		testPath := "/custom/kubeconfig/path"
		t.Setenv("KUBECONFIG", testPath)

		opts := DefaultBaseOptions()
		if opts.KubeconfigPath != testPath {
			t.Errorf("Expected kubeconfig path %s, got %s", testPath, opts.KubeconfigPath)
		}
	})

	t.Run("without KUBECONFIG env var", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "")

		opts := DefaultBaseOptions()
		expectedPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		if opts.KubeconfigPath != expectedPath {
			t.Errorf("Expected kubeconfig path %s, got %s", expectedPath, opts.KubeconfigPath)
		}
	})
}

// TestBindBaseOptions tests base command flag binding.
func TestBindBaseOptions(t *testing.T) {
	opts := DefaultBaseOptions()
	cmd := &cobra.Command{
		Use: "test",
	}

	err := BindBaseOptions(opts, cmd)
	if err != nil {
		t.Fatalf("BindBaseOptions failed: %v", err)
	}

	// Check that base flags were registered
	flags := []string{"kubeconfig"}
	for _, flagName := range flags {
		flag := cmd.Flags().Lookup(flagName)
		if flag == nil {
			t.Errorf("Flag %s was not registered", flagName)
		}
	}
}

// TestValidateBaseOptions tests base options validation.
func TestValidateBaseOptions(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tempDir := t.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte("fake kubeconfig"), 0644); err != nil {
		t.Fatalf("Failed to create test kubeconfig: %v", err)
	}

	testCases := []struct {
		name        string
		setupFunc   func() *RawBaseOptions
		expectError bool
		errorString string
	}{
		{
			name: "valid base options",
			setupFunc: func() *RawBaseOptions {
				return &RawBaseOptions{
					KubeconfigPath: kubeconfigPath,
				}
			},
			expectError: false,
		},
		{
			name: "kubeconfig file not found",
			setupFunc: func() *RawBaseOptions {
				return &RawBaseOptions{
					KubeconfigPath: "/nonexistent/kubeconfig",
				}
			},
			expectError: true,
			errorString: "kubeconfig not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.setupFunc()
			err := ValidateBaseOptions(opts)

			if tc.expectError {
				if err == nil {
					t.Error("Expected validation error, got nil")
				} else if tc.errorString != "" && !containsString(err.Error(), tc.errorString) {
					t.Errorf("Expected error containing '%s', got: %v", tc.errorString, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
