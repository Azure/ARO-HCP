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

package options

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
)

func TestRawUpdateOptions_Validate_ComponentFiltering(t *testing.T) {
	tests := []struct {
		name              string
		components        string
		excludeComponents string
		wantImages        []string
		wantErr           bool
		wantErrMsg        string
	}{
		{
			name:       "no filtering - all components",
			components: "",
			wantImages: []string{"frontend", "backend", "database", "cache"},
			wantErr:    false,
		},
		{
			name:       "filter single component",
			components: "frontend",
			wantImages: []string{"frontend"},
			wantErr:    false,
		},
		{
			name:       "filter multiple components",
			components: "frontend,backend",
			wantImages: []string{"frontend", "backend"},
			wantErr:    false,
		},
		{
			name:       "filter with spaces in comma-separated list",
			components: "frontend, backend, database",
			wantImages: []string{"frontend", "backend", "database"},
			wantErr:    false,
		},
		{
			name:       "filter with extra whitespace",
			components: "  frontend  ,  backend  ",
			wantImages: []string{"frontend", "backend"},
			wantErr:    false,
		},
		{
			name:       "filter non-existent component",
			components: "nonexistent",
			wantErr:    true,
			wantErrMsg: "component \"nonexistent\" not found",
		},
		{
			name:       "filter with one valid and one invalid component",
			components: "frontend,nonexistent",
			wantErr:    true,
			wantErrMsg: "component \"nonexistent\" not found",
		},
		{
			name:              "exclude single component",
			excludeComponents: "frontend",
			wantImages:        []string{"backend", "database", "cache"},
			wantErr:           false,
		},
		{
			name:              "exclude multiple components",
			excludeComponents: "frontend,backend",
			wantImages:        []string{"database", "cache"},
			wantErr:           false,
		},
		{
			name:              "exclude with spaces",
			excludeComponents: "frontend, backend",
			wantImages:        []string{"database", "cache"},
			wantErr:           false,
		},
		{
			name:              "exclude non-existent component",
			excludeComponents: "nonexistent",
			wantErr:           true,
			wantErrMsg:        "excluded component \"nonexistent\" not found",
		},
		{
			name:              "components takes precedence over exclude",
			components:        "frontend",
			excludeComponents: "backend,database",
			wantImages:        []string{"frontend"},
			wantErr:           false,
		},
		{
			name:              "components precedence - exclude is ignored",
			components:        "frontend,backend",
			excludeComponents: "frontend", // This should be ignored
			wantImages:        []string{"frontend", "backend"},
			wantErr:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create test config file
			configPath := createTestConfigFile(t)

			opts := &RawUpdateOptions{
				ConfigPath:        configPath,
				DryRun:            true,
				Components:        tt.components,
				ExcludeComponents: tt.excludeComponents,
			}

			validated, err := opts.Validate(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("Validate() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Check that we have exactly the expected images
			if len(validated.Config.Images) != len(tt.wantImages) {
				t.Errorf("Validate() returned %d images, want %d", len(validated.Config.Images), len(tt.wantImages))
			}

			for _, imageName := range tt.wantImages {
				if _, exists := validated.Config.Images[imageName]; !exists {
					t.Errorf("Validate() missing expected image %s", imageName)
				}
			}

			// Check we don't have unexpected images
			for imageName := range validated.Config.Images {
				found := false
				for _, want := range tt.wantImages {
					if imageName == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Validate() has unexpected image %s", imageName)
				}
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "valid config",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "quay.io/test/app",
						},
						Targets: []config.Target{
							{
								FilePath: "test.yaml",
								JsonPath: "image.digest",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "no images configured",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{},
			},
			wantErr:    true,
			wantErrMsg: "no images configured",
		},
		{
			name: "missing source image",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "",
						},
						Targets: []config.Target{
							{
								FilePath: "test.yaml",
								JsonPath: "image.digest",
							},
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "source image is required",
		},
		{
			name: "no targets configured",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "quay.io/test/app",
						},
						Targets: []config.Target{},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "at least one target is required",
		},
		{
			name: "missing target jsonPath",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "quay.io/test/app",
						},
						Targets: []config.Target{
							{
								FilePath: "test.yaml",
								JsonPath: "",
							},
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "target jsonPath is required",
		},
		{
			name: "missing target filePath",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "quay.io/test/app",
						},
						Targets: []config.Target{
							{
								FilePath: "",
								JsonPath: "image.digest",
							},
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "target filePath is required",
		},
		{
			name: "multiple targets - one valid, one invalid",
			cfg: &config.Config{
				Images: map[string]config.ImageConfig{
					"test": {
						Source: config.Source{
							Image: "quay.io/test/app",
						},
						Targets: []config.Target{
							{
								FilePath: "test1.yaml",
								JsonPath: "image.digest",
							},
							{
								FilePath: "test2.yaml",
								JsonPath: "", // Invalid
							},
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "target jsonPath is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.wantErrMsg != "" {
				if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("validateConfig() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
			}
		})
	}
}

func TestRawUpdateOptions_Validate_LoadErrors(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:       "config file does not exist",
			configPath: "/nonexistent/path/config.yaml",
			wantErr:    true,
			wantErrMsg: "failed to load config",
		},
		{
			name:       "empty config path",
			configPath: "",
			wantErr:    true,
			wantErrMsg: "failed to load config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			opts := &RawUpdateOptions{
				ConfigPath: tt.configPath,
				DryRun:     true,
			}

			_, err := opts.Validate(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.wantErrMsg != "" {
				if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("Validate() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
			}
		})
	}
}

func TestRawUpdateOptions_Validate_InvalidConfig(t *testing.T) {
	t.Run("config validation fails for invalid config", func(t *testing.T) {
		ctx := context.Background()

		// Create a config file with no images
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		content := `images: {}`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create config file: %v", err)
		}

		opts := &RawUpdateOptions{
			ConfigPath: configPath,
			DryRun:     true,
		}

		_, err := opts.Validate(ctx)

		if err == nil {
			t.Error("Validate() expected error for invalid config, got nil")
			return
		}

		if !strings.Contains(err.Error(), "invalid configuration") {
			t.Errorf("Validate() error = %v, should contain 'invalid configuration'", err.Error())
		}
	})
}

// Helper function to create a test config file
func createTestConfigFile(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
images:
  frontend:
    source:
      image: quay.io/test/frontend
    targets:
      - filePath: frontend.yaml
        jsonPath: image.digest
  backend:
    source:
      image: quay.io/test/backend
    targets:
      - filePath: backend.yaml
        jsonPath: image.digest
  database:
    source:
      image: quay.io/test/database
    targets:
      - filePath: db.yaml
        jsonPath: image.digest
  cache:
    source:
      image: quay.io/test/cache
    targets:
      - filePath: cache.yaml
        jsonPath: image.digest
`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test config file: %v", err)
	}

	return configPath
}
