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
	"testing"
)

func TestLoad(t *testing.T) {
	content := `components:
  grafana:
    provider: azure-grafana
    locations: [westus3]
    targets:
    - jsonPath: monitoring.grafanaMajorVersion
      filePath: ../../config/config.yaml
    bumpStrategy: increment
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(cfg.Components))
	}

	comp, ok := cfg.Components["grafana"]
	if !ok {
		t.Fatal("grafana component not found")
	}
	if comp.Provider != "azure-grafana" {
		t.Errorf("expected provider azure-grafana, got %s", comp.Provider)
	}
	if len(comp.Locations) != 1 || comp.Locations[0] != "westus3" {
		t.Errorf("unexpected locations: %v", comp.Locations)
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			"missing provider",
			`components:
  test:
    locations: [westus3]
    targets:
    - jsonPath: foo
      filePath: bar
`,
		},
		{
			"missing locations",
			`components:
  test:
    provider: azure-grafana
    targets:
    - jsonPath: foo
      filePath: bar
`,
		},
		{
			"missing targets",
			`components:
  test:
    provider: azure-grafana
    locations: [westus3]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestReadCurrentVersion(t *testing.T) {
	content := `defaults:
  svc:
    aks:
      kubernetesVersion: "1.35"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	version, err := ReadCurrentVersion(path, "defaults.svc.aks.kubernetesVersion")
	if err != nil {
		t.Fatalf("ReadCurrentVersion failed: %v", err)
	}
	if version != "1.35" {
		t.Errorf("expected 1.35, got %s", version)
	}
}

func TestWriteVersion(t *testing.T) {
	content := `defaults:
  svc:
    aks:
      kubernetesVersion: "1.35"
    istio:
      versions: "asm-1-29"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteVersion(path, "defaults.svc.aks.kubernetesVersion", "1.36"); err != nil {
		t.Fatalf("WriteVersion failed: %v", err)
	}

	version, err := ReadCurrentVersion(path, "defaults.svc.aks.kubernetesVersion")
	if err != nil {
		t.Fatalf("ReadCurrentVersion after write failed: %v", err)
	}
	if version != "1.36" {
		t.Errorf("expected 1.36, got %s", version)
	}

	// Verify other values were not changed
	istioVersion, err := ReadCurrentVersion(path, "defaults.svc.istio.versions")
	if err != nil {
		t.Fatalf("ReadCurrentVersion for istio failed: %v", err)
	}
	if istioVersion != "asm-1-29" {
		t.Errorf("expected asm-1-29, got %s", istioVersion)
	}
}
