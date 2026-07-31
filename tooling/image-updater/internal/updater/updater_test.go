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

func (m *mockRegistryClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, wantMultiArch bool, versionLabel string) (*clients.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Verify the architecture passed is the expected constant (or empty, which defaults to amd64)
	if arch != DefaultArchitecture && arch != "" {
		return nil, fmt.Errorf("unexpected architecture: %s, expected %s", arch, DefaultArchitecture)
	}
	return &clients.Tag{Digest: m.digest, Name: m.tag}, nil
}

func (m *mockRegistryClient) GetDigestForTag(ctx context.Context, repository string, tag string, arch string, wantMultiArch bool, versionLabel string) (*clients.Tag, error) {
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
			wantUpdateNames: []string{"test-image"},
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

			err := u.ProcessImageUpdates(ctx, "test-image", &clients.Tag{Digest: "sha256:newdigest", Name: "v1.0.0"}, tt.target, config.Source{Image: "quay.io/test/app"})

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

func TestUpdater_ProcessImageUpdates_GitHubReleaseRejectsDigestAndSHA(t *testing.T) {
	tests := []struct {
		name       string
		jsonPath   string
		wantErrMsg string
	}{
		{
			name:       "githubLatestRelease with .digest target",
			jsonPath:   "image.digest",
			wantErrMsg: "must not use .digest or .sha paths",
		},
		{
			name:       "githubLatestRelease with .sha target",
			jsonPath:   "image.sha",
			wantErrMsg: "must not use .digest or .sha paths",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logr.NewContext(context.Background(), testLogger())

			tmpDir := t.TempDir()
			yamlPath := filepath.Join(tmpDir, "test.yaml")
			field := "digest"
			if strings.HasSuffix(tt.jsonPath, ".sha") {
				field = "sha"
			}
			yamlContent := fmt.Sprintf("image:\n  %s: oldvalue\n", field)
			if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
				t.Fatalf("failed to create temp yaml: %v", err)
			}

			editor, err := yaml.NewEditor(yamlPath)
			if err != nil {
				t.Fatalf("failed to create yaml editor: %v", err)
			}

			u := &Updater{
				Config:       &config.Config{},
				YAMLEditors:  map[string]yaml.EditorInterface{yamlPath: editor},
				Updates:      make(map[string][]yaml.Update),
				OutputFormat: "table",
			}

			source := config.Source{GitHubLatestRelease: "istio/istio"}
			target := config.Target{FilePath: yamlPath, JsonPath: tt.jsonPath}
			err = u.ProcessImageUpdates(ctx, "test", &clients.Tag{Name: "1.28.3", Version: "1.28.3"}, target, source)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErrMsg)
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
			err = u.ProcessImageUpdates(ctx, "test-image", &clients.Tag{Digest: tt.latestDigest, Name: "v1.0.0"}, target, config.Source{Image: "quay.io/test/app"})
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

// stubMeshLister lets tests drive fetchAzureAKSMeshRevisions without ARM.
type stubMeshLister struct {
	byLocation map[string][]string
	err        error
	calls      []string
}

func (s *stubMeshLister) list(_ context.Context, _ string, location string) ([]string, error) {
	s.calls = append(s.calls, location)
	if s.err != nil {
		return nil, s.err
	}
	revs, ok := s.byLocation[location]
	if !ok {
		return nil, fmt.Errorf("stub: no data for location %q", location)
	}
	return revs, nil
}

func newMeshUpdater(t *testing.T, cfg *config.Config, lister *stubMeshLister) *Updater {
	t.Helper()
	u := New(cfg, false, false, nil, nil, "", "table")
	u.ListMeshRevisions = lister.list
	return u
}

func TestUpdater_FetchAzureAKSMeshRevisions_IntersectAndHighest(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")

	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth", "westus3", "eastus2"},
		},
	}
	lister := &stubMeshLister{byLocation: map[string][]string{
		"uksouth": {"asm-1-28", "asm-1-29", "asm-1-30"},
		"westus3": {"asm-1-28", "asm-1-29"}, // no 1-30 yet
		"eastus2": {"asm-1-28", "asm-1-29", "asm-1-30"},
	}}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	tag, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.Name != "asm-1-29" {
		t.Errorf("got %q, want asm-1-29 (highest common across all locations)", tag.Name)
	}
	if len(lister.calls) != 3 {
		t.Errorf("expected 3 location calls, got %d (%v)", len(lister.calls), lister.calls)
	}
}

func TestUpdater_FetchAzureAKSMeshRevisions_PinBypassesFetch(t *testing.T) {
	// Pin must short-circuit before touching ARM even if env is unset.
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth"},
		},
		PinnedMeshRevision: "asm-1-28",
	}
	lister := &stubMeshLister{err: fmt.Errorf("should not be called")}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	tag, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.Name != "asm-1-28" {
		t.Errorf("got %q, want asm-1-28", tag.Name)
	}
	if len(lister.calls) != 0 {
		t.Errorf("expected 0 ARM calls under pin, got %d", len(lister.calls))
	}
}

func TestUpdater_FetchAzureAKSMeshRevisions_MaxCap(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth"},
		},
		MaxMeshRevision: "asm-1-29",
	}
	lister := &stubMeshLister{byLocation: map[string][]string{
		"uksouth": {"asm-1-28", "asm-1-29", "asm-1-30"},
	}}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	tag, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.Name != "asm-1-29" {
		t.Errorf("got %q, want asm-1-29 (cap applied)", tag.Name)
	}
}

func TestUpdater_FetchAzureAKSMeshRevisions_MissingSubscription(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth"},
		},
	}
	lister := &stubMeshLister{err: fmt.Errorf("should not be called")}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	_, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err == nil || !strings.Contains(err.Error(), "resolve subscription") {
		t.Errorf("expected 'resolve subscription' error, got %v", err)
	}
}

func TestUpdater_FetchAzureAKSMeshRevisions_LocationErrorFailsFast(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth", "westus3"},
		},
	}
	lister := &stubMeshLister{err: fmt.Errorf("transient outage")}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	_, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err == nil || !strings.Contains(err.Error(), "transient outage") {
		t.Errorf("expected transient outage error, got %v", err)
	}
}

func TestUpdater_FetchAzureAKSMeshRevisions_EmptyIntersection(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth", "westus3"},
		},
	}
	lister := &stubMeshLister{byLocation: map[string][]string{
		"uksouth": {"asm-1-30"},
		"westus3": {"asm-1-28"},
	}}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	_, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err == nil || !strings.Contains(err.Error(), "no revision is available in every location") {
		t.Errorf("expected empty-intersection error, got %v", err)
	}
}

// Mirrors the new default-tracking semantics: each region reports a single
// "default" revision, and the tool only advances when every region agrees.
// This is the "wait for the rollout to finish" behaviour we rely on.
func TestUpdater_FetchAzureAKSMeshRevisions_DefaultTracking_MidRolloutHolds(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth", "westus3", "eastus2", "australiaeast"},
		},
	}
	lister := &stubMeshLister{byLocation: map[string][]string{
		"uksouth":       {"asm-1-30"}, // promoted ahead
		"westus3":       {"asm-1-29"},
		"eastus2":       {"asm-1-29"},
		"australiaeast": {"asm-1-29"},
	}}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	_, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err == nil || !strings.Contains(err.Error(), "no revision is available in every location") {
		t.Errorf("expected empty-intersection error while promotion is mid-flight, got %v", err)
	}
}

// Steady state: every region reports the same default → tool advances.
func TestUpdater_FetchAzureAKSMeshRevisions_DefaultTracking_SteadyState(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")
	source := config.Source{
		AzureAKSMeshRevisions: &config.AzureAKSMeshRevisionsSource{
			Locations: []string{"uksouth", "westus3", "eastus2", "australiaeast"},
		},
	}
	lister := &stubMeshLister{byLocation: map[string][]string{
		"uksouth":       {"asm-1-29"},
		"westus3":       {"asm-1-29"},
		"eastus2":       {"asm-1-29"},
		"australiaeast": {"asm-1-29"},
	}}
	u := newMeshUpdater(t, &config.Config{}, lister)
	ctx := logr.NewContext(context.Background(), testLogger())

	got, err := u.fetchAzureAKSMeshRevisions(ctx, testLogger(), source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "asm-1-29" {
		t.Errorf("got %q, want asm-1-29", got.Name)
	}
}
