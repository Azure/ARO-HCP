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

package prometheusrules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	assert.Equal(t, "promtool", opts.PromtoolPath)
	assert.Equal(t, "", opts.ConfigFile)
	assert.False(t, opts.SkipTests)
}

func TestBindOptions(t *testing.T) {
	opts := DefaultOptions()
	cmd := &cobra.Command{}
	require.NoError(t, BindOptions(opts, cmd))

	// config-file flag should exist (validated at runtime, not cobra-level required,
	// because --correlation-map mode uses positional args instead)
	flag := cmd.Flags().Lookup("config-file")
	require.NotNil(t, flag)

	// promtool-path should have a default
	flag = cmd.Flags().Lookup("promtool-path")
	require.NotNil(t, flag)
	assert.Equal(t, "promtool", flag.DefValue)

	// skip-tests should exist
	flag = cmd.Flags().Lookup("skip-tests")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestValidate(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(""), 0644))

	promtoolFile := filepath.Join(t.TempDir(), "promtool")
	require.NoError(t, os.WriteFile(promtoolFile, []byte(""), 0755))

	tests := []struct {
		name        string
		opts        RawOptions
		expectError string
	}{
		{
			name: "valid with tests enabled and explicit promtool path",
			opts: RawOptions{
				ConfigFile:   configFile,
				PromtoolPath: promtoolFile,
			},
		},
		{
			name: "valid with skip-tests and empty promtool path",
			opts: RawOptions{
				ConfigFile: configFile,
				SkipTests:  true,
			},
		},
		{
			name: "empty config file",
			opts: RawOptions{
				PromtoolPath: promtoolFile,
			},
			expectError: "--config-file is required",
		},
		{
			name: "empty promtool path with tests enabled",
			opts: RawOptions{
				ConfigFile:   configFile,
				PromtoolPath: "",
			},
			expectError: "--promtool-path cannot be empty when tests are enabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validated, err := tc.opts.Validate()
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				assert.Nil(t, validated)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, validated)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	configContent := `
prometheusRules:
  rulesFolders: []
  untestedRules: []
  outputBicep: zzz_generated_AlertingRules.bicep
`
	t.Run("valid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

		opts := &RawOptions{
			ConfigFile: configPath,
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.NoError(t, err)
		assert.NotNil(t, completed)
	})

	t.Run("promtool binary not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

		opts := &RawOptions{
			ConfigFile:   configPath,
			PromtoolPath: "definitely-not-a-real-binary-abc123",
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "promtool not found at")
		assert.Nil(t, completed)
	})

	t.Run("config file not found", func(t *testing.T) {
		opts := &RawOptions{
			ConfigFile: "/nonexistent/path/config.yaml",
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configuration file")
		assert.Nil(t, completed)
	})

	t.Run("invalid config content", func(t *testing.T) {
		badConfig := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(badConfig, []byte("not: valid: yaml: ["), 0644))

		opts := &RawOptions{
			ConfigFile: badConfig,
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Nil(t, completed)
	})
}

func TestRun(t *testing.T) {
	configContent := `
prometheusRules:
  rulesFolders: []
  untestedRules: []
  outputBicep: zzz_generated_AlertingRules.bicep
`
	t.Run("generate only with skip-tests", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

		opts := &RawOptions{
			ConfigFile: configPath,
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)
		completed, err := validated.Complete()
		require.NoError(t, err)

		require.NoError(t, completed.Run())

		_, err = os.Stat(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
		assert.NoError(t, err)
	})
}
