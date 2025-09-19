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

func TestEditor_GetValue(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		path        string
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

			got, err := editor.GetValue(tt.path)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("GetValue() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if got != tt.wantValue {
				t.Errorf("GetValue() = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestEditor_SetValue(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		path        string
		newValue    string
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name: "set simple value",
			yamlContent: `
version: v1.0.0
`,
			path:     "version",
			newValue: "v2.0.0",
			wantErr:  false,
		},
		{
			name: "set nested value",
			yamlContent: `
image:
  digest: sha256:old
`,
			path:     "image.digest",
			newValue: "sha256:new",
			wantErr:  false,
		},
		{
			name: "set deeply nested value",
			yamlContent: `
app:
  deployment:
    image:
      digest: sha256:old
`,
			path:     "app.deployment.image.digest",
			newValue: "sha256:new",
			wantErr:  false,
		},
		{
			name: "path does not exist",
			yamlContent: `
version: v1.0.0
`,
			path:       "nonexistent",
			newValue:   "value",
			wantErr:    true,
			wantErrMsg: "path nonexistent not found",
		},
		{
			name: "path points to non-scalar value",
			yamlContent: `
image:
  digest: sha256:abc123
  tag: latest
`,
			path:       "image",
			newValue:   "value",
			wantErr:    true,
			wantErrMsg: "does not point to a scalar value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := createTempYAML(t, tt.yamlContent)
			editor, err := NewEditor(filePath)
			if err != nil {
				t.Fatalf("NewEditor() failed: %v", err)
			}

			err = editor.SetValue(tt.path, tt.newValue)

			if (err != nil) != tt.wantErr {
				t.Errorf("SetValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("SetValue() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			// Verify the value was actually set
			got, err := editor.GetValue(tt.path)
			if err != nil {
				t.Errorf("GetValue() after SetValue() failed: %v", err)
				return
			}
			if got != tt.newValue {
				t.Errorf("After SetValue(), GetValue() = %v, want %v", got, tt.newValue)
			}
		})
	}
}

func TestEditor_Save(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		path        string
		newValue    string
		wantErr     bool
	}{
		{
			name: "save after simple update",
			yamlContent: `
version: v1.0.0
`,
			path:     "version",
			newValue: "v2.0.0",
			wantErr:  false,
		},
		{
			name: "save after nested update",
			yamlContent: `
image:
  digest: sha256:old
  tag: latest
`,
			path:     "image.digest",
			newValue: "sha256:new",
			wantErr:  false,
		},
		{
			name: "save preserves other fields",
			yamlContent: `
version: v1.0.0
image:
  digest: sha256:old
  tag: latest
other: value
`,
			path:     "image.digest",
			newValue: "sha256:new",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := createTempYAML(t, tt.yamlContent)
			editor, err := NewEditor(filePath)
			if err != nil {
				t.Fatalf("NewEditor() failed: %v", err)
			}

			// Make a change
			if err := editor.SetValue(tt.path, tt.newValue); err != nil {
				t.Fatalf("SetValue() failed: %v", err)
			}

			// Save the file
			err = editor.Save()
			if (err != nil) != tt.wantErr {
				t.Errorf("Save() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Verify the file was written correctly by reading it again
			newEditor, err := NewEditor(filePath)
			if err != nil {
				t.Fatalf("NewEditor() after Save() failed: %v", err)
			}

			got, err := newEditor.GetValue(tt.path)
			if err != nil {
				t.Errorf("GetValue() after Save() failed: %v", err)
				return
			}
			if got != tt.newValue {
				t.Errorf("After Save(), GetValue() = %v, want %v", got, tt.newValue)
			}
		})
	}
}

func TestEditor_UpdateFileCorrectly(t *testing.T) {
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

		// Update the digest
		newDigest := "sha256:newdigest456"
		if err := editor.SetValue("image.digest", newDigest); err != nil {
			t.Fatalf("SetValue() failed: %v", err)
		}

		// Save the changes
		if err := editor.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}

		// Re-read the file to verify
		newEditor, err := NewEditor(filePath)
		if err != nil {
			t.Fatalf("NewEditor() after Save() failed: %v", err)
		}

		// Check the updated value
		if got, err := newEditor.GetValue("image.digest"); err != nil {
			t.Errorf("GetValue(image.digest) failed: %v", err)
		} else if got != newDigest {
			t.Errorf("image.digest = %v, want %v", got, newDigest)
		}

		// Verify other values were preserved
		checkValue := func(path, want string) {
			if got, err := newEditor.GetValue(path); err != nil {
				t.Errorf("GetValue(%s) failed: %v", path, err)
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
