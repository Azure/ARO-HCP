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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// mockRegistryClient is a simple mock for testing
type mockRegistryClient struct {
	digest string
	err    error
}

func (m *mockRegistryClient) GetLatestDigest(repository string, tagPattern string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.digest, nil
}

func TestUpdater_UpdateImages(t *testing.T) {
	tests := []struct {
		name            string
		config          *config.Config
		registryDigest  string
		registryError   error
		dryRun          bool
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
			name: "dry run mode does not update",
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
			wantUpdateNames: []string{},
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
			wantErrMsg:     "failed to fetch latest digest",
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
			wantErr:         false,
			wantUpdateNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

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
			yamlEditors := map[string]*yaml.Editor{
				yamlPath: editor,
			}

			mockClient := &mockRegistryClient{
				digest: tt.registryDigest,
				err:    tt.registryError,
			}

			registryClients := map[string]clients.RegistryClient{
				"quay.io": mockClient,
			}

			u := &Updater{
				Config:          tt.config,
				DryRun:          tt.dryRun,
				RegistryClients: registryClients,
				YAMLEditors:     yamlEditors,
				Updates:         make(map[string][]yaml.Update),
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
			ctx := context.Background()

			editor, yamlPath := tt.setupEditor(t)
			tt.target.FilePath = yamlPath

			yamlEditors := make(map[string]*yaml.Editor)
			if editor != nil {
				yamlEditors[yamlPath] = editor
			}

			mockClient := &mockRegistryClient{
				digest: "sha256:newdigest",
			}

			registryClients := map[string]clients.RegistryClient{
				"quay.io": mockClient,
			}

			u := &Updater{
				Config: &config.Config{
					Images: map[string]config.ImageConfig{},
				},
				DryRun:          false,
				RegistryClients: registryClients,
				YAMLEditors:     yamlEditors,
				Updates:         make(map[string][]yaml.Update),
			}

			err := u.ProcessImageUpdates(ctx, "test-image", "sha256:newdigest", tt.target)

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

func TestUpdater_GenerateCommitMessage(t *testing.T) {
	tests := []struct {
		name    string
		updates map[string][]yaml.Update
		want    string
	}{
		{
			name:    "no updates",
			updates: map[string][]yaml.Update{},
			want:    "",
		},
		{
			name: "single update",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{Name: "frontend", OldDigest: "sha256:old123", NewDigest: "sha256:abc123"},
				},
			},
			want: "Updated images for dev/int:\nfrontend: sha256:old123 -> sha256:abc123",
		},
		{
			name: "multiple updates",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{Name: "frontend", OldDigest: "sha256:old123", NewDigest: "sha256:abc123"},
					{Name: "backend", OldDigest: "sha256:old456", NewDigest: "sha256:def456"},
				},
			},
			want: "Updated images for dev/int:\nfrontend: sha256:old123 -> sha256:abc123\nbackend: sha256:old456 -> sha256:def456",
		},
		{
			name: "duplicate updates - both shown",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{Name: "frontend", OldDigest: "sha256:old123", NewDigest: "sha256:abc123"},
					{Name: "frontend", OldDigest: "sha256:old123", NewDigest: "sha256:abc123"},
				},
			},
			want: "Updated images for dev/int:\nfrontend: sha256:old123 -> sha256:abc123\nfrontend: sha256:old123 -> sha256:abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Updater{
				Updates: tt.updates,
			}

			got := u.GenerateCommitMessage()

			// For multiple updates, the order might vary due to map iteration
			// So we check that the message contains all expected parts
			if tt.name == "multiple updates" || tt.name == "duplicate updates - both shown" {
				if !strings.Contains(got, "Updated images for dev/int:") {
					t.Errorf("GenerateCommitMessage() missing header")
				}
				for _, updates := range tt.updates {
					for _, update := range updates {
						expected := fmt.Sprintf("%s: %s -> %s", update.Name, update.OldDigest, update.NewDigest)
						if !strings.Contains(got, expected) {
							t.Errorf("GenerateCommitMessage() missing update: %s", expected)
						}
					}
				}
			} else {
				if got != tt.want {
					t.Errorf("GenerateCommitMessage() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestUpdater_FileUpdateIntegration(t *testing.T) {
	t.Run("complete file update workflow", func(t *testing.T) {
		ctx := context.Background()

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

		registryClients := map[string]clients.RegistryClient{
			"quay.io": mockClient,
		}

		// Create updater
		u := &Updater{
			Config:          cfg,
			DryRun:          false,
			RegistryClients: registryClients,
			YAMLEditors: map[string]*yaml.Editor{
				yamlPath: editor,
			},
			Updates: make(map[string][]yaml.Update),
		}

		// Run update
		if err := u.UpdateImages(ctx); err != nil {
			t.Fatalf("UpdateImages() failed: %v", err)
		}

		// Verify the file was updated correctly
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
