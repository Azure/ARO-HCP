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

package clients

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeDockerConfig(t *testing.T) {
	tests := []struct {
		name           string
		existingConfig map[string]any
		kvConfig       map[string]any
		wantAuths      map[string]any
		wantErr        bool
	}{
		{
			name:           "merge with empty existing config",
			existingConfig: map[string]any{},
			kvConfig: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "dGVzdDp0ZXN0",
					},
				},
			},
			wantAuths: map[string]any{
				"quay.io": map[string]any{
					"auth": "dGVzdDp0ZXN0",
				},
			},
			wantErr: false,
		},
		{
			name: "merge with existing auths",
			existingConfig: map[string]any{
				"auths": map[string]any{
					"docker.io": map[string]any{
						"auth": "ZG9ja2VyOnRlc3Q=",
					},
				},
			},
			kvConfig: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "cXVheTp0ZXN0",
					},
				},
			},
			wantAuths: map[string]any{
				"docker.io": map[string]any{
					"auth": "ZG9ja2VyOnRlc3Q=",
				},
				"quay.io": map[string]any{
					"auth": "cXVheTp0ZXN0",
				},
			},
			wantErr: false,
		},
		{
			name: "overwrite existing registry auth",
			existingConfig: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "b2xkOnRlc3Q=",
					},
				},
			},
			kvConfig: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "bmV3OnRlc3Q=",
					},
				},
			},
			wantAuths: map[string]any{
				"quay.io": map[string]any{
					"auth": "bmV3OnRlc3Q=",
				},
			},
			wantErr: false,
		},
		{
			name:           "kv config without auths section",
			existingConfig: map[string]any{},
			kvConfig: map[string]any{
				"credHelpers": map[string]any{
					"gcr.io": "gcloud",
				},
			},
			wantAuths: nil,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory for test
			tmpDir := t.TempDir()
			dockerDir := filepath.Join(tmpDir, ".docker")
			if err := os.MkdirAll(dockerDir, 0700); err != nil {
				t.Fatalf("failed to create .docker directory: %v", err)
			}

			// Override home directory for this test
			oldHome := os.Getenv("HOME")
			os.Setenv("HOME", tmpDir)
			defer os.Setenv("HOME", oldHome)

			// Write existing config if provided
			configPath := filepath.Join(dockerDir, "config.json")
			if len(tt.existingConfig) > 0 {
				data, err := json.MarshalIndent(tt.existingConfig, "", "  ")
				if err != nil {
					t.Fatalf("failed to marshal existing config: %v", err)
				}
				if err := os.WriteFile(configPath, data, 0600); err != nil {
					t.Fatalf("failed to write existing config: %v", err)
				}
			}

			// Run merge
			err := mergeDockerConfig(tt.kvConfig)

			if (err != nil) != tt.wantErr {
				t.Errorf("mergeDockerConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Read and verify merged config
			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("failed to read merged config: %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("failed to unmarshal merged config: %v", err)
			}

			if tt.wantAuths == nil {
				if _, exists := got["auths"]; exists {
					t.Errorf("mergeDockerConfig() created auths section when it shouldn't have")
				}
				return
			}

			gotAuths, ok := got["auths"].(map[string]any)
			if !ok {
				t.Fatalf("merged config auths is not a map")
			}

			// Compare auths
			if len(gotAuths) != len(tt.wantAuths) {
				t.Errorf("mergeDockerConfig() auths count = %v, want %v", len(gotAuths), len(tt.wantAuths))
			}

			for registry, wantAuth := range tt.wantAuths {
				gotAuth, exists := gotAuths[registry]
				if !exists {
					t.Errorf("mergeDockerConfig() missing registry %s", registry)
					continue
				}

				gotAuthStr, _ := json.Marshal(gotAuth)
				wantAuthStr, _ := json.Marshal(wantAuth)
				if string(gotAuthStr) != string(wantAuthStr) {
					t.Errorf("mergeDockerConfig() registry %s = %v, want %v", registry, string(gotAuthStr), string(wantAuthStr))
				}
			}
		})
	}
}

func TestDecodeSecretValue(t *testing.T) {
	tests := []struct {
		name        string
		secretValue string
		wantDecoded map[string]any
		wantErr     bool
	}{
		{
			name: "base64 encoded JSON",
			secretValue: base64.StdEncoding.EncodeToString([]byte(`{
				"auths": {
					"quay.io": {
						"auth": "dGVzdDp0ZXN0"
					}
				}
			}`)),
			wantDecoded: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "dGVzdDp0ZXN0",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "raw JSON (not base64)",
			secretValue: `{
				"auths": {
					"quay.io": {
						"auth": "cXVheTp0ZXN0"
					}
				}
			}`,
			wantDecoded: map[string]any{
				"auths": map[string]any{
					"quay.io": map[string]any{
						"auth": "cXVheTp0ZXN0",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Try to decode as base64 first
			var dockerConfigData []byte
			decoded, err := base64.StdEncoding.DecodeString(tt.secretValue)
			if err == nil {
				dockerConfigData = decoded
			} else {
				dockerConfigData = []byte(tt.secretValue)
			}

			// Parse JSON
			var got map[string]any
			err = json.Unmarshal(dockerConfigData, &got)

			if (err != nil) != tt.wantErr {
				t.Errorf("decode error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Compare decoded data
			gotStr, _ := json.Marshal(got)
			wantStr, _ := json.Marshal(tt.wantDecoded)
			if string(gotStr) != string(wantStr) {
				t.Errorf("decoded = %v, want %v", string(gotStr), string(wantStr))
			}
		})
	}
}
