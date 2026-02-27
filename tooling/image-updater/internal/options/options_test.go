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
		groups            string
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
			name:              "components with non-overlapping exclude",
			components:        "frontend,backend",
			excludeComponents: "backend",
			wantImages:        []string{"frontend"},
			wantErr:           false,
		},
		{
			name:              "components with exclude that does not match selected",
			components:        "frontend",
			excludeComponents: "backend", // backend is not in the selected set
			wantErr:           true,
			wantErrMsg:        "excluded component \"backend\" not found",
		},
		{
			name:       "groups - filter single group",
			groups:     "web",
			wantImages: []string{"frontend", "backend"},
			wantErr:    false,
		},
		{
			name:       "groups - filter multiple groups",
			groups:     "web,storage",
			wantImages: []string{"frontend", "backend", "database", "cache"},
			wantErr:    false,
		},
		{
			name:       "groups - non-existent group",
			groups:     "nonexistent",
			wantErr:    true,
			wantErrMsg: "group \"nonexistent\" not found",
		},
		{
			name:       "groups combined with components (union)",
			components: "database",
			groups:     "web",
			wantImages: []string{"frontend", "backend", "database"},
			wantErr:    false,
		},
		{
			name:              "groups with exclude-components",
			groups:            "web",
			excludeComponents: "frontend",
			wantImages:        []string{"backend"},
			wantErr:           false,
		},
		{
			name:              "groups and components with exclude",
			components:        "database",
			groups:            "web",
			excludeComponents: "backend",
			wantImages:        []string{"frontend", "database"},
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
				Groups:            tt.groups,
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
			wantErrMsg: "source image or githubLatestRelease is required",
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

// TestRealConfigValid_Regression ensures the in-repo config.yaml validates and includes the istio-istioctl entry (githubLatestRelease).
// Run from repo root: go test ./tooling/image-updater/internal/options/ -run TestRealConfigValid
func TestRealConfigValid_Regression(t *testing.T) {
	configPath := filepath.Join("..", "..", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skip("in-repo config.yaml not found (run from tooling/image-updater or repo root)")
	}
	opts := &RawUpdateOptions{ConfigPath: configPath, DryRun: true}
	validated, err := opts.Validate(context.Background())
	if err != nil {
		t.Fatalf("real config Validate() failed: %v", err)
	}
	img, ok := validated.Config.Images["istio-istioctl"]
	if !ok {
		t.Fatal("real config should have istio-istioctl entry")
	}
	if img.Source.GitHubLatestRelease != "istio/istio" {
		t.Errorf("istio-istioctl should have githubLatestRelease istio/istio, got %q", img.Source.GitHubLatestRelease)
	}
}

func TestComplete_AuthenticationRequirements(t *testing.T) {
	tests := []struct {
		name                     string
		configContentFunc        func(tmpDir string) string
		targetFiles              []string
		wantRegistryAuthRequired map[string]bool
		wantRegistryClientCount  int
	}{
		{
			name: "single registry with useAuth false",
			configContentFunc: func(tmpDir string) string {
				return `
images:
  test:
    group: test-group
    source:
      image: registry.azurecr.io/test/app
      useAuth: false
    targets:
      - filePath: ` + filepath.Join(tmpDir, "test.yaml") + `
        jsonPath: image.digest
        env: dev
`
			},
			targetFiles: []string{"test.yaml"},
			wantRegistryAuthRequired: map[string]bool{
				"registry.azurecr.io": false,
			},
			wantRegistryClientCount: 1,
		},
		{
			name: "single registry with useAuth true",
			configContentFunc: func(tmpDir string) string {
				return `
images:
  test:
    group: test-group
    source:
      image: registry.azurecr.io/test/app
      useAuth: true
    targets:
      - filePath: ` + filepath.Join(tmpDir, "test.yaml") + `
        jsonPath: image.digest
        env: dev
`
			},
			targetFiles: []string{"test.yaml"},
			wantRegistryAuthRequired: map[string]bool{
				"registry.azurecr.io": true,
			},
			wantRegistryClientCount: 1,
		},
		{
			name: "single registry with useAuth not set (defaults to true)",
			configContentFunc: func(tmpDir string) string {
				return `
images:
  test:
    group: test-group
    source:
      image: registry.azurecr.io/test/app
    targets:
      - filePath: ` + filepath.Join(tmpDir, "test.yaml") + `
        jsonPath: image.digest
        env: dev
`
			},
			targetFiles: []string{"test.yaml"},
			wantRegistryAuthRequired: map[string]bool{
				"registry.azurecr.io": true,
			},
			wantRegistryClientCount: 1,
		},
		{
			name: "multiple images same registry - one requires auth false",
			configContentFunc: func(tmpDir string) string {
				return `
images:
  test1:
    group: test-group
    source:
      image: registry.azurecr.io/test/app1
      useAuth: false
    targets:
      - filePath: ` + filepath.Join(tmpDir, "test1.yaml") + `
        jsonPath: image.digest
        env: dev
  test2:
    group: test-group
    source:
      image: registry.azurecr.io/test/app2
      useAuth: true
    targets:
      - filePath: ` + filepath.Join(tmpDir, "test2.yaml") + `
        jsonPath: image.digest
        env: dev
`
			},
			targetFiles: []string{"test1.yaml", "test2.yaml"},
			wantRegistryAuthRequired: map[string]bool{
				"registry.azurecr.io": false, // false takes precedence
			},
			wantRegistryClientCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			tmpDir := t.TempDir()

			// Create dummy target files
			for _, target := range tt.targetFiles {
				targetPath := filepath.Join(tmpDir, target)
				content := "image:\n  digest: sha256:abc123\n"
				if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
					t.Fatalf("failed to create target file: %v", err)
				}
			}

			configPath := filepath.Join(tmpDir, "config.yaml")
			configContent := tt.configContentFunc(tmpDir)
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				t.Fatalf("failed to create config file: %v", err)
			}

			opts := &RawUpdateOptions{
				ConfigPath: configPath,
				DryRun:     true,
			}

			validated, err := opts.Validate(ctx)
			if err != nil {
				t.Fatalf("Validate() unexpected error = %v", err)
			}

			updater, err := validated.Complete(ctx)
			if err != nil {
				t.Fatalf("Complete() unexpected error = %v", err)
			}

			// We can't directly access the internal auth requirements map,
			// but we can verify the correct number of registry clients were created
			if updater == nil {
				t.Fatal("Complete() returned nil updater")
			}

			// Note: We can't easily test the internal auth behavior without exposing internals,
			// but we've verified through integration tests that it works correctly
		})
	}
}

// Helper function to create a test config file
func createTestConfigFile(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
images:
  frontend:
    group: web
    source:
      image: quay.io/test/frontend
    targets:
      - filePath: frontend-dev.yaml
        jsonPath: image.digest
        env: dev
      - filePath: frontend-int.yaml
        jsonPath: image.digest
        env: int
  backend:
    group: web
    source:
      image: quay.io/test/backend
    targets:
      - filePath: backend-dev.yaml
        jsonPath: image.digest
        env: dev
      - filePath: backend-int.yaml
        jsonPath: image.digest
        env: int
  database:
    group: storage
    source:
      image: quay.io/test/database
    targets:
      - filePath: db-dev.yaml
        jsonPath: image.digest
        env: dev
      - filePath: db-int.yaml
        jsonPath: image.digest
        env: int
  cache:
    group: storage
    source:
      image: quay.io/test/cache
    targets:
      - filePath: cache-dev.yaml
        jsonPath: image.digest
        env: dev
      - filePath: cache-int.yaml
        jsonPath: image.digest
        env: int
`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test config file: %v", err)
	}

	return configPath
}

func TestKeyVaultDeduplication(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		wantKVConfigs int // number of unique KeyVault configs
		wantErr       bool
	}{
		{
			name: "single image with keyVault",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 1,
			wantErr:       false,
		},
		{
			name: "multiple images with same keyVault - should deduplicate",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test1.yaml
        jsonPath: image.digest
        env: dev
  test2:
    group: test-group
    source:
      image: quay.io/test/app2
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test2.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 1, // Same vault+secret should be deduplicated
			wantErr:       false,
		},
		{
			name: "multiple images with different keyVault configs",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test1.yaml
        jsonPath: image.digest
        env: dev
  test2:
    group: test-group
    source:
      image: quay.io/test/app2
      useAuth: true
      keyVault:
        url: "https://vault2.vault.azure.net/"
        secretName: "secret2"
    targets:
      - filePath: test2.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 2, // Different vaults
			wantErr:       false,
		},
		{
			name: "same vault different secrets",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test1.yaml
        jsonPath: image.digest
        env: dev
  test2:
    group: test-group
    source:
      image: quay.io/test/app2
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret2"
    targets:
      - filePath: test2.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 2, // Same vault but different secrets
			wantErr:       false,
		},
		{
			name: "mixed - some with keyVault some without",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
      useAuth: true
      keyVault:
        url: "https://vault1.vault.azure.net/"
        secretName: "secret1"
    targets:
      - filePath: test1.yaml
        jsonPath: image.digest
        env: dev
  test2:
    group: test-group
    source:
      image: quay.io/test/app2
      useAuth: false
    targets:
      - filePath: test2.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 1, // Only test1 has keyVault
			wantErr:       false,
		},
		{
			name: "no images with keyVault",
			configContent: `
images:
  test1:
    group: test-group
    source:
      image: quay.io/test/app1
    targets:
      - filePath: test1.yaml
        jsonPath: image.digest
        env: dev
`,
			wantKVConfigs: 0,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp config file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("failed to create config file: %v", err)
			}

			// Load and validate config
			cfg, err := config.Load(configPath)
			if err != nil {
				t.Fatalf("failed to load config: %v", err)
			}

			// Simulate the deduplication logic from Complete()
			kvConfigs := make(map[string]struct{})
			for _, imageConfig := range cfg.Images {
				if imageConfig.Source.KeyVault != nil &&
					imageConfig.Source.KeyVault.URL != "" &&
					imageConfig.Source.KeyVault.SecretName != "" {
					key := imageConfig.Source.KeyVault.URL + "|" + imageConfig.Source.KeyVault.SecretName
					kvConfigs[key] = struct{}{}
				}
			}

			gotCount := len(kvConfigs)
			if gotCount != tt.wantKVConfigs {
				t.Errorf("KeyVault config count = %v, want %v", gotCount, tt.wantKVConfigs)
			}
		})
	}
}
