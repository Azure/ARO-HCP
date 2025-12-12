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

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFilterByComponent(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"frontend": {
				Source: Source{Image: "quay.io/test/frontend"},
				Targets: []Target{
					{FilePath: "frontend.yaml", JsonPath: "image.digest"},
				},
			},
			"backend": {
				Source: Source{Image: "quay.io/test/backend"},
				Targets: []Target{
					{FilePath: "backend.yaml", JsonPath: "image.digest"},
				},
			},
			"database": {
				Source: Source{Image: "quay.io/test/database"},
				Targets: []Target{
					{FilePath: "db.yaml", JsonPath: "image.digest"},
				},
			},
		},
	}

	tests := []struct {
		name           string
		componentName  string
		wantComponents []string
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "filter single valid component",
			componentName:  "frontend",
			wantComponents: []string{"frontend"},
			wantErr:        false,
		},
		{
			name:           "filter another valid component",
			componentName:  "backend",
			wantComponents: []string{"backend"},
			wantErr:        false,
		},
		{
			name:           "empty component name returns all",
			componentName:  "",
			wantComponents: []string{"frontend", "backend", "database"},
			wantErr:        false,
		},
		{
			name:           "non-existent component returns error",
			componentName:  "nonexistent",
			wantComponents: []string{},
			wantErr:        true,
			wantErrMsg:     "component \"nonexistent\" not found in configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.FilterByComponent(tt.componentName)

			if (err != nil) != tt.wantErr {
				t.Errorf("FilterByComponent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("FilterByComponent() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Get actual component names
			var gotComponents []string
			for name := range got.Images {
				gotComponents = append(gotComponents, name)
			}
			slices.Sort(gotComponents)

			// Sort expected for comparison
			wantComponents := make([]string, len(tt.wantComponents))
			copy(wantComponents, tt.wantComponents)
			slices.Sort(wantComponents)

			// Compare
			if !slices.Equal(gotComponents, wantComponents) {
				t.Errorf("FilterByComponent() = %v, want %v", gotComponents, wantComponents)
			}
		})
	}
}

func TestFilterByComponents(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"frontend": {
				Source: Source{Image: "quay.io/test/frontend"},
				Targets: []Target{
					{FilePath: "frontend.yaml", JsonPath: "image.digest"},
				},
			},
			"backend": {
				Source: Source{Image: "quay.io/test/backend"},
				Targets: []Target{
					{FilePath: "backend.yaml", JsonPath: "image.digest"},
				},
			},
			"database": {
				Source: Source{Image: "quay.io/test/database"},
				Targets: []Target{
					{FilePath: "db.yaml", JsonPath: "image.digest"},
				},
			},
			"cache": {
				Source: Source{Image: "quay.io/test/cache"},
				Targets: []Target{
					{FilePath: "cache.yaml", JsonPath: "image.digest"},
				},
			},
		},
	}

	tests := []struct {
		name           string
		componentNames []string
		wantComponents []string
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "filter multiple valid components",
			componentNames: []string{"frontend", "backend"},
			wantComponents: []string{"frontend", "backend"},
			wantErr:        false,
		},
		{
			name:           "filter single component",
			componentNames: []string{"database"},
			wantComponents: []string{"database"},
			wantErr:        false,
		},
		{
			name:           "filter all components",
			componentNames: []string{"frontend", "backend", "database", "cache"},
			wantComponents: []string{"frontend", "backend", "database", "cache"},
			wantErr:        false,
		},
		{
			name:           "empty list returns all",
			componentNames: []string{},
			wantComponents: []string{"frontend", "backend", "database", "cache"},
			wantErr:        false,
		},
		{
			name:           "nil list returns all",
			componentNames: nil,
			wantComponents: []string{"frontend", "backend", "database", "cache"},
			wantErr:        false,
		},
		{
			name:           "non-existent component in list returns error",
			componentNames: []string{"frontend", "nonexistent"},
			wantComponents: []string{},
			wantErr:        true,
			wantErrMsg:     "component \"nonexistent\" not found in configuration",
		},
		{
			name:           "all components non-existent",
			componentNames: []string{"foo", "bar"},
			wantComponents: []string{},
			wantErr:        true,
			wantErrMsg:     "component \"foo\" not found in configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.FilterByComponents(tt.componentNames)

			if (err != nil) != tt.wantErr {
				t.Errorf("FilterByComponents() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("FilterByComponents() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Get actual component names
			var gotComponents []string
			for name := range got.Images {
				gotComponents = append(gotComponents, name)
			}
			slices.Sort(gotComponents)

			// Sort expected for comparison
			wantComponents := make([]string, len(tt.wantComponents))
			copy(wantComponents, tt.wantComponents)
			slices.Sort(wantComponents)

			// Compare
			if !slices.Equal(gotComponents, wantComponents) {
				t.Errorf("FilterByComponents() = %v, want %v", gotComponents, wantComponents)
			}
		})
	}
}

func TestFilterExcludingComponents(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"frontend": {
				Source: Source{Image: "quay.io/test/frontend"},
				Targets: []Target{
					{FilePath: "frontend.yaml", JsonPath: "image.digest"},
				},
			},
			"backend": {
				Source: Source{Image: "quay.io/test/backend"},
				Targets: []Target{
					{FilePath: "backend.yaml", JsonPath: "image.digest"},
				},
			},
			"database": {
				Source: Source{Image: "quay.io/test/database"},
				Targets: []Target{
					{FilePath: "db.yaml", JsonPath: "image.digest"},
				},
			},
			"cache": {
				Source: Source{Image: "quay.io/test/cache"},
				Targets: []Target{
					{FilePath: "cache.yaml", JsonPath: "image.digest"},
				},
			},
		},
	}

	tests := []struct {
		name              string
		excludeComponents []string
		wantMissing       []string
		wantPresent       []string
		wantErr           bool
		wantErrMsg        string
	}{
		{
			name:              "exclude single component",
			excludeComponents: []string{"frontend"},
			wantMissing:       []string{"frontend"},
			wantPresent:       []string{"backend", "database", "cache"},
			wantErr:           false,
		},
		{
			name:              "exclude multiple components",
			excludeComponents: []string{"frontend", "backend"},
			wantMissing:       []string{"frontend", "backend"},
			wantPresent:       []string{"database", "cache"},
			wantErr:           false,
		},
		{
			name:              "exclude all but one",
			excludeComponents: []string{"frontend", "backend", "database"},
			wantMissing:       []string{"frontend", "backend", "database"},
			wantPresent:       []string{"cache"},
			wantErr:           false,
		},
		{
			name:              "empty exclusion list returns all",
			excludeComponents: []string{},
			wantPresent:       []string{"frontend", "backend", "database", "cache"},
			wantErr:           false,
		},
		{
			name:              "nil exclusion list returns all",
			excludeComponents: nil,
			wantPresent:       []string{"frontend", "backend", "database", "cache"},
			wantErr:           false,
		},
		{
			name:              "non-existent component in exclusion list returns error",
			excludeComponents: []string{"frontend", "nonexistent"},
			wantErr:           true,
			wantErrMsg:        "excluded component \"nonexistent\" not found in configuration",
		},
		{
			name:              "exclude all components returns error - catches typo",
			excludeComponents: []string{"frontend", "backend", "database", "cachee"}, // typo: cachee instead of cache
			wantErr:           true,
			wantErrMsg:        "excluded component \"cachee\" not found in configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.FilterExcludingComponents(tt.excludeComponents)

			if (err != nil) != tt.wantErr {
				t.Errorf("FilterExcludingComponents() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("FilterExcludingComponents() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Get actual component names
			var gotComponents []string
			for name := range got.Images {
				gotComponents = append(gotComponents, name)
			}
			slices.Sort(gotComponents)

			// Sort expected for comparison
			wantComponents := make([]string, len(tt.wantPresent))
			copy(wantComponents, tt.wantPresent)
			slices.Sort(wantComponents)

			// Compare
			if !slices.Equal(gotComponents, wantComponents) {
				t.Errorf("FilterExcludingComponents() = %v, want %v", gotComponents, wantComponents)
			}

			// Verify excluded components are not present
			for _, component := range tt.wantMissing {
				if _, exists := got.Images[component]; exists {
					t.Errorf("FilterExcludingComponents() should not include excluded component %s", component)
				}
			}
		})
	}
}

func TestConfigLoad(t *testing.T) {
	tests := []struct {
		name           string
		setupFile      func(t *testing.T) string
		wantErr        bool
		wantErrMsg     string
		wantImageNames []string
	}{
		{
			name: "valid config file",
			setupFile: func(t *testing.T) string {
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
`
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatalf("failed to create config file: %v", err)
				}
				return configPath
			},
			wantErr:        false,
			wantImageNames: []string{"frontend", "backend"},
		},
		{
			name: "file does not exist",
			setupFile: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent.yaml")
			},
			wantErr:    true,
			wantErrMsg: "failed to read config file",
		},
		{
			name: "invalid YAML syntax",
			setupFile: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "invalid.yaml")
				content := "images:\n  frontend: [unclosed"
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatalf("failed to create config file: %v", err)
				}
				return configPath
			},
			wantErr:    true,
			wantErrMsg: "failed to parse config file",
		},
		{
			name: "empty config file",
			setupFile: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "empty.yaml")
				if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
					t.Fatalf("failed to create config file: %v", err)
				}
				return configPath
			},
			wantErr:        false,
			wantImageNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.setupFile(t)

			got, err := Load(configPath)

			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("Load() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Get actual image names
			var gotImages []string
			for name := range got.Images {
				gotImages = append(gotImages, name)
			}
			slices.Sort(gotImages)

			// Sort expected for comparison
			wantImages := make([]string, len(tt.wantImageNames))
			copy(wantImages, tt.wantImageNames)
			slices.Sort(wantImages)

			// Compare
			if !slices.Equal(gotImages, wantImages) {
				t.Errorf("Load() = %v, want %v", gotImages, wantImages)
			}
		})
	}
}

func TestConfigLoad_WithUseAuth(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		wantImageName string
		wantUseAuth   *bool
	}{
		{
			name: "useAuth set to false",
			configContent: `
images:
  test:
    source:
      image: registry.azurecr.io/test/app
      useAuth: false
    targets:
      - filePath: test.yaml
        jsonPath: image.digest
`,
			wantImageName: "test",
			wantUseAuth:   boolPtr(false),
		},
		{
			name: "useAuth set to true",
			configContent: `
images:
  test:
    source:
      image: registry.azurecr.io/test/app
      useAuth: true
    targets:
      - filePath: test.yaml
        jsonPath: image.digest
`,
			wantImageName: "test",
			wantUseAuth:   boolPtr(true),
		},
		{
			name: "useAuth not set (defaults to nil)",
			configContent: `
images:
  test:
    source:
      image: registry.azurecr.io/test/app
    targets:
      - filePath: test.yaml
        jsonPath: image.digest
`,
			wantImageName: "test",
			wantUseAuth:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("failed to create config file: %v", err)
			}

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() unexpected error = %v", err)
			}

			img, exists := cfg.Images[tt.wantImageName]
			if !exists {
				t.Fatalf("Load() missing expected image %s", tt.wantImageName)
			}

			if tt.wantUseAuth == nil {
				if img.Source.UseAuth != nil {
					t.Errorf("Load() UseAuth = %v, want nil", img.Source.UseAuth)
				}
			} else {
				if img.Source.UseAuth == nil {
					t.Errorf("Load() UseAuth = nil, want %v", *tt.wantUseAuth)
				} else if *img.Source.UseAuth != *tt.wantUseAuth {
					t.Errorf("Load() UseAuth = %v, want %v", *img.Source.UseAuth, *tt.wantUseAuth)
				}
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func TestConfigLoad_WithKeyVault(t *testing.T) {
	tests := []struct {
		name              string
		configContent     string
		wantImageName     string
		wantKeyVaultURL   string
		wantKeyVaultName  string
		wantKeyVaultIsNil bool
	}{
		{
			name: "keyVault configured for image",
			configContent: `
images:
  clusters-service:
    source:
      image: quay.io/app-sre/aro-hcp-clusters-service
      useAuth: true
      keyVault:
        url: "https://arohcpdev-global.vault.azure.net/"
        secretName: "component-sync-pull-secret"
    targets:
      - filePath: config.yaml
        jsonPath: image.digest
`,
			wantImageName:     "clusters-service",
			wantKeyVaultURL:   "https://arohcpdev-global.vault.azure.net/",
			wantKeyVaultName:  "component-sync-pull-secret",
			wantKeyVaultIsNil: false,
		},
		{
			name: "keyVault not configured",
			configContent: `
images:
  maestro:
    source:
      image: quay.io/maestro/maestro
      useAuth: false
    targets:
      - filePath: config.yaml
        jsonPath: image.digest
`,
			wantImageName:     "maestro",
			wantKeyVaultIsNil: true,
		},
		{
			name: "keyVault with empty fields",
			configContent: `
images:
  test:
    source:
      image: quay.io/test/app
      keyVault:
        url: ""
        secretName: ""
    targets:
      - filePath: config.yaml
        jsonPath: image.digest
`,
			wantImageName:     "test",
			wantKeyVaultURL:   "",
			wantKeyVaultName:  "",
			wantKeyVaultIsNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("failed to create config file: %v", err)
			}

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() unexpected error = %v", err)
			}

			img, exists := cfg.Images[tt.wantImageName]
			if !exists {
				t.Fatalf("Load() missing expected image %s", tt.wantImageName)
			}

			if tt.wantKeyVaultIsNil {
				if img.Source.KeyVault != nil {
					t.Errorf("Load() KeyVault = %v, want nil", img.Source.KeyVault)
				}
			} else {
				if img.Source.KeyVault == nil {
					t.Errorf("Load() KeyVault = nil, want non-nil")
					return
				}
				if img.Source.KeyVault.URL != tt.wantKeyVaultURL {
					t.Errorf("Load() KeyVault.URL = %v, want %v", img.Source.KeyVault.URL, tt.wantKeyVaultURL)
				}
				if img.Source.KeyVault.SecretName != tt.wantKeyVaultName {
					t.Errorf("Load() KeyVault.SecretName = %v, want %v", img.Source.KeyVault.SecretName, tt.wantKeyVaultName)
				}
			}
		})
	}
}

func TestSource_ParseImageReference(t *testing.T) {
	tests := []struct {
		name           string
		image          string
		wantRegistry   string
		wantRepository string
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "valid quay.io image",
			image:          "quay.io/organization/repository",
			wantRegistry:   "quay.io",
			wantRepository: "organization/repository",
			wantErr:        false,
		},
		{
			name:           "valid ACR image",
			image:          "myregistry.azurecr.io/myapp",
			wantRegistry:   "myregistry.azurecr.io",
			wantRepository: "myapp",
			wantErr:        false,
		},
		{
			name:           "image with nested repository path",
			image:          "quay.io/org/team/app",
			wantRegistry:   "quay.io",
			wantRepository: "org/team/app",
			wantErr:        false,
		},
		{
			name:       "empty image reference",
			image:      "",
			wantErr:    true,
			wantErrMsg: "image reference is empty",
		},
		{
			name:       "missing registry",
			image:      "repository",
			wantErr:    true,
			wantErrMsg: "invalid image reference",
		},
		{
			name:       "missing repository",
			image:      "quay.io/",
			wantErr:    true,
			wantErrMsg: "repository part is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Source{Image: tt.image}

			gotRegistry, gotRepository, err := s.ParseImageReference()

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseImageReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("ParseImageReference() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if gotRegistry != tt.wantRegistry {
				t.Errorf("ParseImageReference() registry = %v, want %v", gotRegistry, tt.wantRegistry)
			}
			if gotRepository != tt.wantRepository {
				t.Errorf("ParseImageReference() repository = %v, want %v", gotRepository, tt.wantRepository)
			}
		})
	}
}

func TestFilterByEnvironments(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"frontend": {
				Source: Source{Image: "quay.io/test/frontend"},
				Targets: []Target{
					{FilePath: "frontend-dev.yaml", JsonPath: "image.digest", Env: "dev"},
					{FilePath: "frontend-int.yaml", JsonPath: "image.digest", Env: "int"},
					{FilePath: "frontend-stg.yaml", JsonPath: "image.digest", Env: "stg"},
					{FilePath: "frontend-prod.yaml", JsonPath: "image.digest", Env: "prod"},
				},
			},
			"backend": {
				Source: Source{Image: "quay.io/test/backend"},
				Targets: []Target{
					{FilePath: "backend-dev.yaml", JsonPath: "image.digest", Env: "dev"},
					{FilePath: "backend-int.yaml", JsonPath: "image.digest", Env: "int"},
				},
			},
			"database": {
				Source: Source{Image: "quay.io/test/database"},
				Targets: []Target{
					{FilePath: "db-stg.yaml", JsonPath: "image.digest", Env: "stg"},
					{FilePath: "db-prod.yaml", JsonPath: "image.digest", Env: "prod"},
				},
			},
		},
	}

	tests := []struct {
		name         string
		environments []string
		wantTargets  map[string][]string // component -> list of target file paths
	}{
		{
			name:         "filter dev environment",
			environments: []string{"dev"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-dev.yaml"},
				"backend":  {"backend-dev.yaml"},
			},
		},
		{
			name:         "filter int environment",
			environments: []string{"int"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-int.yaml"},
				"backend":  {"backend-int.yaml"},
			},
		},
		{
			name:         "filter stg environment",
			environments: []string{"stg"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-stg.yaml"},
				"database": {"db-stg.yaml"},
			},
		},
		{
			name:         "filter prod environment",
			environments: []string{"prod"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-prod.yaml"},
				"database": {"db-prod.yaml"},
			},
		},
		{
			name:         "filter multiple environments - dev and int",
			environments: []string{"dev", "int"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-dev.yaml", "frontend-int.yaml"},
				"backend":  {"backend-dev.yaml", "backend-int.yaml"},
			},
		},
		{
			name:         "filter multiple environments - int and stg (promotion mode)",
			environments: []string{"int", "stg"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-int.yaml", "frontend-stg.yaml"},
				"backend":  {"backend-int.yaml"},
				"database": {"db-stg.yaml"},
			},
		},
		{
			name:         "filter multiple environments - stg and prod (promotion mode)",
			environments: []string{"stg", "prod"},
			wantTargets: map[string][]string{
				"frontend": {"frontend-stg.yaml", "frontend-prod.yaml"},
				"database": {"db-stg.yaml", "db-prod.yaml"},
			},
		},
		{
			name:         "empty environments list returns original config",
			environments: []string{},
			wantTargets: map[string][]string{
				"frontend": {"frontend-dev.yaml", "frontend-int.yaml", "frontend-stg.yaml", "frontend-prod.yaml"},
				"backend":  {"backend-dev.yaml", "backend-int.yaml"},
				"database": {"db-stg.yaml", "db-prod.yaml"},
			},
		},
		{
			name:         "non-matching environment returns error",
			environments: []string{"qa"},
			wantTargets:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.FilterByEnvironments(tt.environments)
			
			// Handle error cases
			if tt.wantTargets == nil {
				if err == nil {
					t.Errorf("FilterByEnvironments() expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Fatalf("FilterByEnvironments() unexpected error: %v", err)
			}

			// Check that we got the expected number of components
			if len(got.Images) != len(tt.wantTargets) {
				t.Errorf("FilterByEnvironments() returned %d components, want %d", len(got.Images), len(tt.wantTargets))
			}

			// Check each component
			for componentName, wantFilePaths := range tt.wantTargets {
				imageConfig, exists := got.Images[componentName]
				if !exists {
					t.Errorf("FilterByEnvironments() missing component %s", componentName)
					continue
				}

				// Get actual file paths
				var gotFilePaths []string
				for _, target := range imageConfig.Targets {
					gotFilePaths = append(gotFilePaths, target.FilePath)
				}
				slices.Sort(gotFilePaths)

				// Sort expected for comparison
				wantFilePathsSorted := make([]string, len(wantFilePaths))
				copy(wantFilePathsSorted, wantFilePaths)
				slices.Sort(wantFilePathsSorted)

				// Compare
				if !slices.Equal(gotFilePaths, wantFilePathsSorted) {
					t.Errorf("FilterByEnvironments() component %s targets = %v, want %v", componentName, gotFilePaths, wantFilePathsSorted)
				}
			}

			// Check for unexpected components
			for componentName := range got.Images {
				if _, expected := tt.wantTargets[componentName]; !expected {
					t.Errorf("FilterByEnvironments() returned unexpected component %s", componentName)
				}
			}
		})
	}
}
