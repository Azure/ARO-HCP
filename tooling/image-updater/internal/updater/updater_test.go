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

package updater

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// testLogger creates a logger for tests
func testLogger() logr.Logger {
	return logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError, // Only show errors in tests
	}))
}

// mockRegistryClient is a simple mock for testing
type mockRegistryClient struct {
	digest string
	tag    string
	err    error
}

func (m *mockRegistryClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool, versionLabel string) (*clients.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Verify the architecture passed is the expected constant (or empty, which defaults to amd64)
	if arch != DefaultArchitecture && arch != "" {
		return nil, fmt.Errorf("unexpected architecture: %s, expected %s", arch, DefaultArchitecture)
	}
	return &clients.Tag{Digest: m.digest, Name: m.tag}, nil
}

func (m *mockRegistryClient) GetDigestForTag(ctx context.Context, repository string, tag string, arch string, multiArch bool, versionLabel string) (*clients.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Verify the architecture passed is the expected constant (or empty, which defaults to amd64)
	if arch != DefaultArchitecture && arch != "" {
		return nil, fmt.Errorf("unexpected architecture: %s, expected %s", arch, DefaultArchitecture)
	}
	return &clients.Tag{Digest: m.digest, Name: tag}, nil
}

func TestUpdater_UpdateImages(t *testing.T) {
	tests := []struct {
		name            string
		config          *config.Config
		registryDigest  string
		registryError   error
		dryRun          bool
		forceUpdate     bool
		wantErr         bool
		wantErrMsg      string
		wantUpdateNames []string
	}{
		{
			name: "successful update",
			config: &config.Config{
				Images: map[string]config.ImageConfig{
					"test-image": {
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
			registryDigest:  "sha256:newdigest",
			dryRun:          false,
			wantErr:         false,
			wantUpdateNames: []string{"test-image"},
		},
		{
			name: "dry run mode does not update files but tracks changes",
			config: &config.Config{
				Images: map[string]config.ImageConfig{
					"test-image": {
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
			registryDigest:  "sha256:newdigest",
			dryRun:          true,
			wantErr:         false,
			wantUpdateNames: []string{"test-image"}, // Changed: dry-run now tracks updates for reporting
		},
		{
			name: "registry fetch error",
			config: &config.Config{
				Images: map[string]config.ImageConfig{
					"test-image": {
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
			registryDigest: "",
			registryError:  fmt.Errorf("registry unavailable"),
			wantErr:        true,
			wantErrMsg:     "failed to fetch latest value",
		},
		{
			name: "no update when digest is same",
			config: &config.Config{
				Images: map[string]config.ImageConfig{
					"test-image": {
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
			registryDigest:  "sha256:olddigest",
			dryRun:          false,
			forceUpdate:     false,
			wantErr:         false,
			wantUpdateNames: []string{},
		},
		{
			name: "force update when digest is same",
			config: &config.Config{
				Images: map[string]config.ImageConfig{
					"test-image": {
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
			registryDigest:  "sha256:olddigest",
			dryRun:          false,
			forceUpdate:     true,
			wantErr:         false,
			wantUpdateNames: []string{"test-image"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logr.NewContext(context.Background(), testLogger())

			tmpDir := t.TempDir()
			yamlPath := filepath.Join(tmpDir, "test.yaml")
			yamlContent := `
image:
  digest: sha256:olddigest
`
			if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
				t.Fatalf("failed to create temp yaml: %v", err)
			}

			for name, imgCfg := range tt.config.Images {
				for i := range imgCfg.Targets {
					imgCfg.Targets[i].FilePath = yamlPath
				}
				tt.config.Images[name] = imgCfg
			}

			editor, err := yaml.NewEditor(yamlPath)
			if err != nil {
				t.Fatalf("failed to create yaml editor: %v", err)
			}
			yamlEditors := map[string]yaml.EditorInterface{
				yamlPath: editor,
			}

			mockClient := &mockRegistryClient{
				digest: tt.registryDigest,
				err:    tt.registryError,
			}

			// Registry client key format is "registry:useAuth"
			registryClients := map[string]clients.RegistryClient{
				"quay.io:false": mockClient,
			}

			u := &Updater{
				Config:          tt.config,
				DryRun:          tt.dryRun,
				ForceUpdate:     tt.forceUpdate,
				RegistryClients: registryClients,
				YAMLEditors:     yamlEditors,
				Updates:         make(map[string][]yaml.Update),
				OutputFormat:    "table",
			}

			err = u.UpdateImages(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateImages() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("UpdateImages() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Count total updates across all files
			totalUpdates := 0
			for _, updates := range u.Updates {
				totalUpdates += len(updates)
			}

			if totalUpdates != len(tt.wantUpdateNames) {
				t.Errorf("UpdateImages() got %d updates, want %d", totalUpdates, len(tt.wantUpdateNames))
			}

			// Check that all expected updates are present
			for _, updateName := range tt.wantUpdateNames {
				found := false
				for _, updates := range u.Updates {
					for _, update := range updates {
						if update.Name == updateName {
							found = true
							if update.NewDigest != tt.registryDigest {
								t.Errorf("Update %s has digest %s, want %s", updateName, update.NewDigest, tt.registryDigest)
							}
							break
						}
					}
					if found {
						break
					}
				}
				if !found {
					t.Errorf("UpdateImages() missing expected update for %s", updateName)
				}
			}

			// Check that there are no unexpected updates
			for _, updates := range u.Updates {
				for _, update := range updates {
					found := false
					for _, want := range tt.wantUpdateNames {
						if update.Name == want {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("UpdateImages() has unexpected update for %s", update.Name)
					}
				}
			}

			if !tt.dryRun && len(tt.wantUpdateNames) > 0 {
				newEditor, err := yaml.NewEditor(yamlPath)
				if err != nil {
					t.Fatalf("failed to read updated yaml: %v", err)
				}

				_, digest, err := newEditor.GetUpdate("image.digest")
				if err != nil {
					t.Fatalf("failed to get digest from updated yaml: %v", err)
				}

				if digest != tt.registryDigest {
					t.Errorf("updated digest = %v, want %v", digest, tt.registryDigest)
				}
			}
		})
	}
}

func TestUpdater_UpdateImage_ErrorCases(t *testing.T) {
	tests := []struct {
		name        string
		setupEditor func(t *testing.T) (*yaml.Editor, string)
		target      config.Target
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name: "yaml editor not available",
			setupEditor: func(t *testing.T) (*yaml.Editor, string) {
				// Return nil to simulate missing editor
				return nil, "nonexistent.yaml"
			},
			target: config.Target{
				FilePath: "nonexistent.yaml",
				JsonPath: "image.digest",
			},
			wantErr:    true,
			wantErrMsg: "no YAML editor available",
		},
		{
			name: "json path does not exist",
			setupEditor: func(t *testing.T) (*yaml.Editor, string) {
				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "test.yaml")
				yamlContent := `
image:
  tag: latest
`
				if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
					t.Fatalf("failed to create temp yaml: %v", err)
				}
				editor, err := yaml.NewEditor(yamlPath)
				if err != nil {
					t.Fatalf("failed to create editor: %v", err)
				}
				return editor, yamlPath
			},
			target: config.Target{
				FilePath: "test.yaml", // Will be overridden
				JsonPath: "image.nonexistent",
			},
			wantErr:    true,
			wantErrMsg: "failed to get current digest",
		},
		{
			name: "json path points to non-scalar",
			setupEditor: func(t *testing.T) (*yaml.Editor, string) {
				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "test.yaml")
				yamlContent := `
image:
  digest: sha256:abc
  tag: latest
`
				if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
					t.Fatalf("failed to create temp yaml: %v", err)
				}
				editor, err := yaml.NewEditor(yamlPath)
				if err != nil {
					t.Fatalf("failed to create editor: %v", err)
				}
				return editor, yamlPath
			},
			target: config.Target{
				FilePath: "test.yaml",
				JsonPath: "image", // Points to map, not scalar
			},
			wantErr:    true,
			wantErrMsg: "failed to get current digest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logr.NewContext(context.Background(), testLogger())

			editor, yamlPath := tt.setupEditor(t)
			tt.target.FilePath = yamlPath

			yamlEditors := make(map[string]yaml.EditorInterface)
			if editor != nil {
				yamlEditors[yamlPath] = editor
			}

			mockClient := &mockRegistryClient{
				digest: "sha256:newdigest",
			}

			// Registry client key format is "registry:useAuth"
			registryClients := map[string]clients.RegistryClient{
				"quay.io:false": mockClient,
			}

			u := &Updater{
				Config: &config.Config{
					Images: map[string]config.ImageConfig{},
				},
				DryRun:          false,
				RegistryClients: registryClients,
				YAMLEditors:     yamlEditors,
				Updates:         make(map[string][]yaml.Update),
				OutputFormat:    "table",
			}

			_, err := u.ProcessImageUpdates(ctx, "test-image", &clients.Tag{Digest: "sha256:newdigest", Name: "v1.0.0"}, tt.target)

			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessImageUpdates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.wantErrMsg != "" {
				if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("ProcessImageUpdates() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
			}
		})
	}
}

func TestUpdater_ProcessImageUpdates_SHAFieldHandling(t *testing.T) {
	tests := []struct {
		name              string
		jsonPath          string
		currentValue      string
		latestDigest      string
		wantDigestInFile  string
		wantUpdateDigest  string
		wantUpdateCreated bool
	}{
		{
			name:              "sha field strips sha256 prefix",
			jsonPath:          "image.sha",
			currentValue:      "olddigest123",
			latestDigest:      "sha256:newdigest456",
			wantDigestInFile:  "newdigest456",
			wantUpdateDigest:  "newdigest456",
			wantUpdateCreated: true,
		},
		{
			name:              "digest field keeps sha256 prefix",
			jsonPath:          "image.digest",
			currentValue:      "sha256:olddigest123",
			latestDigest:      "sha256:newdigest456",
			wantDigestInFile:  "sha256:newdigest456",
			wantUpdateDigest:  "sha256:newdigest456",
			wantUpdateCreated: true,
		},
		{
			name:              "sha field no update when digests match",
			jsonPath:          "image.sha",
			currentValue:      "abc123",
			latestDigest:      "sha256:abc123",
			wantDigestInFile:  "abc123",
			wantUpdateDigest:  "",
			wantUpdateCreated: false,
		},
		{
			name:              "digest field no update when digests match",
			jsonPath:          "image.digest",
			currentValue:      "sha256:abc123",
			latestDigest:      "sha256:abc123",
			wantDigestInFile:  "sha256:abc123",
			wantUpdateDigest:  "",
			wantUpdateCreated: false,
		},
		{
			name:              "nested sha field path",
			jsonPath:          "prometheus.prometheusOperator.image.sha",
			currentValue:      "oldsha",
			latestDigest:      "sha256:newsha",
			wantDigestInFile:  "newsha",
			wantUpdateDigest:  "newsha",
			wantUpdateCreated: true,
		},
		{
			name:              "sha field with already stripped digest",
			jsonPath:          "image.sha",
			currentValue:      "olddigest",
			latestDigest:      "newdigest",
			wantDigestInFile:  "newdigest",
			wantUpdateDigest:  "newdigest",
			wantUpdateCreated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logr.NewContext(context.Background(), testLogger())

			// Create temp YAML file with initial content
			tmpDir := t.TempDir()
			yamlPath := filepath.Join(tmpDir, "test.yaml")

			// Build YAML content based on jsonPath
			var yamlContent string
			if strings.HasPrefix(tt.jsonPath, "prometheus.") {
				yamlContent = fmt.Sprintf(`
prometheus:
  prometheusOperator:
    image:
      sha: %s
`, tt.currentValue)
			} else {
				// Set the appropriate field
				if strings.Contains(tt.jsonPath, ".sha") {
					yamlContent = fmt.Sprintf(`
image:
  sha: %s
`, tt.currentValue)
				} else {
					yamlContent = fmt.Sprintf(`
image:
  digest: %s
`, tt.currentValue)
				}
			}

			if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
				t.Fatalf("failed to create temp yaml: %v", err)
			}

			// Create YAML editor
			editor, err := yaml.NewEditor(yamlPath)
			if err != nil {
				t.Fatalf("failed to create yaml editor: %v", err)
			}

			yamlEditors := map[string]yaml.EditorInterface{
				yamlPath: editor,
			}

			// Create target
			target := config.Target{
				FilePath: yamlPath,
				JsonPath: tt.jsonPath,
			}

			// Create updater
			u := &Updater{
				Config:       &config.Config{},
				DryRun:       false,
				YAMLEditors:  yamlEditors,
				Updates:      make(map[string][]yaml.Update),
				OutputFormat: "table",
			}

			// Process update
			_, err = u.ProcessImageUpdates(ctx, "test-image", &clients.Tag{Digest: tt.latestDigest, Name: "v1.0.0"}, target)
			if err != nil {
				t.Fatalf("ProcessImageUpdates() failed: %v", err)
			}

			// Verify update was or wasn't created
			totalUpdates := 0
			var update *yaml.Update
			for _, updates := range u.Updates {
				totalUpdates += len(updates)
				if len(updates) > 0 {
					update = &updates[0]
				}
			}

			if tt.wantUpdateCreated && totalUpdates == 0 {
				t.Errorf("Expected update to be created, but none was created")
			}
			if !tt.wantUpdateCreated && totalUpdates > 0 {
				t.Errorf("Expected no update, but %d update(s) created", totalUpdates)
			}

			// If update was created, verify the digest format
			if tt.wantUpdateCreated && update != nil {
				if update.NewDigest != tt.wantUpdateDigest {
					t.Errorf("Update.NewDigest = %v, want %v", update.NewDigest, tt.wantUpdateDigest)
				}

				// Apply the update
				if err := editor.ApplyUpdates(u.Updates[yamlPath]); err != nil {
					t.Fatalf("ApplyUpdates() failed: %v", err)
				}

				// Verify file content
				newEditor, err := yaml.NewEditor(yamlPath)
				if err != nil {
					t.Fatalf("failed to read updated file: %v", err)
				}

				_, fileDigest, err := newEditor.GetUpdate(tt.jsonPath)
				if err != nil {
					t.Fatalf("failed to get digest from file: %v", err)
				}

				if fileDigest != tt.wantDigestInFile {
					t.Errorf("File digest = %v, want %v", fileDigest, tt.wantDigestInFile)
				}
			}
		})
	}
}

func TestUpdater_FileUpdateIntegration(t *testing.T) {
	t.Run("complete file update workflow", func(t *testing.T) {
		ctx := logr.NewContext(context.Background(), testLogger())

		// Create temp YAML file with initial content
		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "app.yaml")
		initialContent := `
metadata:
  name: myapp
image:
  digest: sha256:olddigest123
  tag: latest
config:
  replicas: 3
`
		if err := os.WriteFile(yamlPath, []byte(initialContent), 0644); err != nil {
			t.Fatalf("failed to create temp yaml: %v", err)
		}

		// Setup config
		cfg := &config.Config{
			Images: map[string]config.ImageConfig{
				"myapp": {
					Source: config.Source{
						Image: "quay.io/test/myapp",
					},
					Targets: []config.Target{
						{
							FilePath: yamlPath,
							JsonPath: "image.digest",
						},
					},
				},
			},
		}

		// Create YAML editor
		editor, err := yaml.NewEditor(yamlPath)
		if err != nil {
			t.Fatalf("failed to create yaml editor: %v", err)
		}

		// Create mock registry
		newDigest := "sha256:newdigest456"
		mockClient := &mockRegistryClient{
			digest: newDigest,
		}

		// Registry client key format is "registry:useAuth"
		// Since UseAuth is not set in the config, it defaults to false
		registryClients := map[string]clients.RegistryClient{
			"quay.io:false": mockClient,
		}

		// Create updater
		u := &Updater{
			Config:          cfg,
			DryRun:          false,
			RegistryClients: registryClients,
			YAMLEditors: map[string]yaml.EditorInterface{
				yamlPath: editor,
			},
			Updates:      make(map[string][]yaml.Update),
			OutputFormat: "table",
		}

		// Run update
		err = u.UpdateImages(ctx)
		if err != nil {
			t.Fatalf("UpdateImages() failed: %v", err)
		}

		// Read updated file to verify changes
		newEditor, err := yaml.NewEditor(yamlPath)
		if err != nil {
			t.Fatalf("failed to read updated file: %v", err)
		}

		// Check updated value
		_, digest, err := newEditor.GetUpdate("image.digest")
		if err != nil {
			t.Fatalf("failed to get digest: %v", err)
		}
		if digest != newDigest {
			t.Errorf("digest = %v, want %v", digest, newDigest)
		}

		// Verify other fields were preserved
		checkValue := func(path, want string) {
			if _, got, err := newEditor.GetUpdate(path); err != nil {
				t.Errorf("GetUpdate(%s) failed: %v", path, err)
			} else if got != want {
				t.Errorf("%s = %v, want %v", path, got, want)
			}
		}

		checkValue("metadata.name", "myapp")
		checkValue("image.tag", "latest")
		checkValue("config.replicas", "3")

		// Verify updates were recorded
		wantUpdateNames := []string{"myapp"}
		totalUpdates := 0
		for _, updates := range u.Updates {
			totalUpdates += len(updates)
		}
		if totalUpdates != len(wantUpdateNames) {
			t.Errorf("Updates count = %d, want %d", totalUpdates, len(wantUpdateNames))
		}

		for _, wantName := range wantUpdateNames {
			found := false
			for _, updates := range u.Updates {
				for _, update := range updates {
					if update.Name == wantName {
						found = true
						if update.NewDigest != newDigest {
							t.Errorf("Update %s digest = %v, want %v", wantName, update.NewDigest, newDigest)
						}
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("Missing expected update for %s", wantName)
			}
		}
	})
}
