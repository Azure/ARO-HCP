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

package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewEditor(t *testing.T) {
	tests := []struct {
		name       string
		setupFile  func(t *testing.T) string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "valid YAML file",
			setupFile: func(t *testing.T) string {
				return createTempYAML(t, `
version: v1.0.0
image:
  digest: sha256:abc123
`)
			},
			wantErr: false,
		},
		{
			name: "file does not exist",
			setupFile: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent.yaml")
			},
			wantErr:    true,
			wantErrMsg: "failed to read file",
		},
		{
			name: "invalid YAML syntax",
			setupFile: func(t *testing.T) string {
				tmpDir := t.TempDir()
				filePath := filepath.Join(tmpDir, "invalid.yaml")
				// Create a truly invalid YAML file
				if err := os.WriteFile(filePath, []byte("version: v1.0.0\n\t\tinvalid: [unclosed"), 0644); err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				return filePath
			},
			wantErr:    true,
			wantErrMsg: "failed to parse YAML file",
		},
		{
			name: "empty file",
			setupFile: func(t *testing.T) string {
				return createTempYAML(t, "")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFile(t)
			editor, err := NewEditor(filePath)

			if (err != nil) != tt.wantErr {
				t.Errorf("NewEditor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("NewEditor() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if editor == nil {
				t.Error("NewEditor() returned nil editor for valid file")
				return
			}
			if editor.filePath != filePath {
				t.Errorf("NewEditor() filePath = %v, want %v", editor.filePath, filePath)
			}
		})
	}
}

func TestEditor_GetUpdate(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		path        string
		wantLine    int
		wantValue   string
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name: "get simple value",
			yamlContent: `
version: v1.0.0
`,
			path:      "version",
			wantLine:  2,
			wantValue: "v1.0.0",
			wantErr:   false,
		},
		{
			name: "get nested value",
			yamlContent: `
image:
  digest: sha256:abc123
`,
			path:      "image.digest",
			wantLine:  3,
			wantValue: "sha256:abc123",
			wantErr:   false,
		},
		{
			name: "get deeply nested value",
			yamlContent: `
app:
  deployment:
    image:
      digest: sha256:deep
`,
			path:      "app.deployment.image.digest",
			wantLine:  5,
			wantValue: "sha256:deep",
			wantErr:   false,
		},
		{
			name: "path does not exist - top level",
			yamlContent: `
version: v1.0.0
`,
			path:       "nonexistent",
			wantErr:    true,
			wantErrMsg: "path nonexistent not found",
		},
		{
			name: "path does not exist - nested",
			yamlContent: `
image:
  digest: sha256:abc123
`,
			path:       "image.nonexistent",
			wantErr:    true,
			wantErrMsg: "path image.nonexistent not found",
		},
		{
			name: "path points to non-scalar value (map)",
			yamlContent: `
image:
  digest: sha256:abc123
  tag: latest
`,
			path:       "image",
			wantErr:    true,
			wantErrMsg: "does not point to a scalar value",
		},
		{
			name: "path points to non-scalar value (list)",
			yamlContent: `
tags:
  - v1.0.0
  - v2.0.0
`,
			path:       "tags",
			wantErr:    true,
			wantErrMsg: "does not point to a scalar value",
		},
		{
			name: "empty path component in middle",
			yamlContent: `
a:
  b:
    c: value
`,
			path:       "a..c",
			wantErr:    true,
			wantErrMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := createTempYAML(t, tt.yamlContent)
			editor, err := NewEditor(filePath)
			if err != nil {
				t.Fatalf("NewEditor() failed: %v", err)
			}

			gotLine, gotValue, err := editor.GetUpdate(tt.path)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("GetUpdate() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if gotLine != tt.wantLine {
				t.Errorf("GetUpdate() line = %v, want %v", gotLine, tt.wantLine)
			}
			if gotValue != tt.wantValue {
				t.Errorf("GetUpdate() value = %v, want %v", gotValue, tt.wantValue)
			}
		})
	}
}

func TestEditor_ApplyUpdates(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		updates     []Update
		wantContent string
		wantErr     bool
	}{
		{
			name: "single update",
			yamlContent: `
version: v1.0.0
image:
  digest: sha256:old123
`,
			updates: []Update{
				{Line: 4, OldDigest: "sha256:old123", NewDigest: "sha256:new456"},
			},
			wantContent: `
version: v1.0.0
image:
  digest: sha256:new456
`,
		},
		{
			name: "multiple updates",
			yamlContent: `
app1:
  digest: sha256:old1
app2:
  digest: sha256:old2
app3:
  digest: sha256:old3
`,
			updates: []Update{
				{Line: 3, OldDigest: "sha256:old1", NewDigest: "sha256:new1"},
				{Line: 5, OldDigest: "sha256:old2", NewDigest: "sha256:new2"},
				{Line: 7, OldDigest: "sha256:old3", NewDigest: "sha256:new3"},
			},
			wantContent: `
app1:
  digest: sha256:new1
app2:
  digest: sha256:new2
app3:
  digest: sha256:new3
`,
		},
		{
			name: "updates out of order",
			yamlContent: `
app1:
  digest: sha256:old1
app2:
  digest: sha256:old2
`,
			updates: []Update{
				{Line: 5, OldDigest: "sha256:old2", NewDigest: "sha256:new2"},
				{Line: 3, OldDigest: "sha256:old1", NewDigest: "sha256:new1"},
			},
			wantContent: `
app1:
  digest: sha256:new1
app2:
  digest: sha256:new2
`,
		},
		{
			name:        "empty updates",
			yamlContent: `version: v1.0.0`,
			updates:     []Update{},
			wantContent: `version: v1.0.0`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := createTempYAML(t, tt.yamlContent)
			editor, err := NewEditor(filePath)
			if err != nil {
				t.Fatalf("NewEditor() failed: %v", err)
			}

			err = editor.ApplyUpdates(tt.updates)

			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyUpdates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Read the file to verify the updates were applied
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read file after ApplyUpdates(): %v", err)
			}

			if string(content) != tt.wantContent {
				t.Errorf("ApplyUpdates() file content:\ngot:\n%s\nwant:\n%s", string(content), tt.wantContent)
			}
		})
	}
}

func TestEditor_ApplyUpdatesPreservesFormatting(t *testing.T) {
	yamlContent := `
metadata:
  name: test-app
  version: v1.0.0
image:
  repository: myregistry.io/myapp
  digest: sha256:olddigest123
  tag: latest
config:
  replicas: 3
  port: 8080
`

	t.Run("update preserves file structure and other values", func(t *testing.T) {
		filePath := createTempYAML(t, yamlContent)
		editor, err := NewEditor(filePath)
		if err != nil {
			t.Fatalf("NewEditor() failed: %v", err)
		}

		// Apply update to the digest
		updates := []Update{
			{Line: 7, OldDigest: "sha256:olddigest123", NewDigest: "sha256:newdigest456"},
		}
		if err := editor.ApplyUpdates(updates); err != nil {
			t.Fatalf("ApplyUpdates() failed: %v", err)
		}

		// Re-read the file to verify
		newEditor, err := NewEditor(filePath)
		if err != nil {
			t.Fatalf("NewEditor() after ApplyUpdates() failed: %v", err)
		}

		// Check the updated value
		if _, got, err := newEditor.GetUpdate("image.digest"); err != nil {
			t.Errorf("GetUpdate(image.digest) failed: %v", err)
		} else if got != "sha256:newdigest456" {
			t.Errorf("image.digest = %v, want %v", got, "sha256:newdigest456")
		}

		// Verify other values were preserved using GetUpdate
		checkValue := func(path, want string) {
			if _, got, err := newEditor.GetUpdate(path); err != nil {
				t.Errorf("GetUpdate(%s) failed: %v", path, err)
			} else if got != want {
				t.Errorf("%s = %v, want %v", path, got, want)
			}
		}

		checkValue("metadata.name", "test-app")
		checkValue("metadata.version", "v1.0.0")
		checkValue("image.repository", "myregistry.io/myapp")
		checkValue("image.tag", "latest")
		checkValue("config.replicas", "3")
		checkValue("config.port", "8080")
	})
}

// Helper function to create a temporary YAML file for testing
func createTempYAML(t *testing.T, content string) string {
	t.Helper()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	return filePath
}
