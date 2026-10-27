// Copyright 2026 Microsoft Corporation
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

	"gopkg.in/yaml.v3"
)

func TestLoadValidationErrors(t *testing.T) {
	const valid = `components:
  istio:
    provider: azure-istio
    locations: [westus3]
    kubernetesVersionPath: defaults.svc.aks.kubernetesVersion
    targets:
    - jsonPath: defaults.svc.istio.versions
      filePath: ../../config/config.yaml
`
	tests := []struct {
		name, old, replacement, wantError string
	}{
		{"missing provider", "    provider: azure-istio\n", "", "unsupported provider"},
		{"unsupported provider", "azure-istio", "azure-grafana", "unsupported provider"},
		{"missing locations", "    locations: [westus3]\n", "", "at least one location"},
		{"duplicate locations", "[westus3]", "[westus3, westus3]", "empty or duplicate location"},
		{"missing targets", "    targets:\n    - jsonPath: defaults.svc.istio.versions\n      filePath: ../../config/config.yaml\n", "", "exactly one target"},
		{"missing AKS path", "    kubernetesVersionPath: defaults.svc.aks.kubernetesVersion\n", "", "kubernetesVersionPath is required"},
		{"unknown field", "components:", "unknown: true\ncomponents:", "field unknown not found"},
		{"invalid pause flag", "    provider:", "    paused: invalid\n    provider:", "cannot unmarshal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			content := strings.Replace(valid, tt.old, tt.replacement, 1)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected validation error %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestWriteVersionPreservesSurroundingText(t *testing.T) {
	for _, tt := range []struct {
		name, before, after string
	}{
		{"double quotes", "istio: {previousVersion: \"asm-1-29\", versions: \"asm-1-29\"}\n", "istio: {previousVersion: \"asm-1-29\", versions: \"asm-1-30\"}\n"},
		{"single quotes and CRLF", "istio:\r\n  versions: 'asm-1-29' # retain this comment\r\n", "istio:\r\n  versions: 'asm-1-30' # retain this comment\r\n"},
		{"unquoted", "istio: {previousVersion: asm-1-29, versions: asm-1-29}\n", "istio: {previousVersion: asm-1-29, versions: asm-1-30}\n"},
		{"Go template", "region: {{ .ctx.region }}\nistio: {versions: \"asm-1-29\"}\n", "region: {{ .ctx.region }}\nistio: {versions: \"asm-1-30\"}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.before), 0600); err != nil {
				t.Fatal(err)
			}
			if err := WriteVersion(path, "istio.versions", "asm-1-29", "asm-1-30"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != tt.after {
				t.Fatalf("edit must change only the target scalar:\ngot %q, %v\nwant %q", got, err, tt.after)
			}
		})
	}
}

func TestWriteVersionRejectsChangedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("version: asm-1-30\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteVersion(path, "version", "asm-1-29", "asm-1-30"); err == nil {
		t.Fatal("expected stale configuration error")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(content) {
		t.Fatalf("failed write changed file: %q, %v", got, err)
	}
}

func TestLoadShippedConfiguration(t *testing.T) {
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Components) != 1 || len(cfg.Components["istio-mesh-revision"].Targets) != 1 {
		t.Fatalf("expected an Istio-only configuration: %+v", cfg)
	}
	component := cfg.Components["istio-mesh-revision"]
	if !slices.Equal(component.Locations, []string{"all"}) || component.Targets[0].JsonPath != "defaults.svc.istio.versions" {
		t.Fatalf("shipped config must discover all regions and target the shared Istio version: %+v", component)
	}
	component.Paused = true
	cfg.Components["istio-mesh-revision"] = component
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "paused.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || !cfg.Components["istio-mesh-revision"].Paused {
		t.Fatalf("pause flag must be loaded from configuration: %+v, %v", cfg, err)
	}
}
