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

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRewriteGenerateBoskosAndValidateBoskosConfig(t *testing.T) {
	t.Parallel()

	catalog := &Catalog{
		Version: 1,
		Environments: map[string]Environment{
			"dev": {
				DeployEnvs: []string{"prow", "ci01"},
				Pools: []Pool{
					{
						SubscriptionName:        "dev",
						Region:                  "westus3",
						ResourceType:            "aro-hcp-dev-westus3-slot",
						SlotCount:               1,
						IdentityContainerPrefix: "aro-hcp-msi-container-dev",
						IdentityContainerCount:  20,
					},
				},
			},
			"int": {
				DeployEnvs: []string{"int"},
				Pools: []Pool{
					{
						SubscriptionName:        "int",
						Region:                  "uksouth",
						ResourceType:            "aro-hcp-int-uksouth-slot",
						SlotCount:               1,
						IdentityContainerPrefix: "aro-hcp-msi-container-int",
						IdentityContainerCount:  20,
					},
				},
			},
			"prod": {
				DeployEnvs: []string{"prod"},
				Pools: []Pool{
					{
						SubscriptionName:        "prod",
						Region:                  "uksouth",
						ResourceType:            "aro-hcp-prod-uksouth-slot",
						SlotCount:               2,
						IdentityContainerPrefix: "aro-hcp-msi-container-prod",
						IdentityContainerCount:  15,
					},
				},
			},
		},
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("expected synthetic catalog to validate: %v", err)
	}

	releaseRepo := t.TempDir()
	configDir := filepath.Join(releaseRepo, "core-services", "prow", "02_config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("expected config dir creation to succeed: %v", err)
	}

	generateBoskosContent := strings.Join([]string{
		"CONFIG = {",
		BoskosTypesBeginMarker,
		"    'old-type': {},",
		BoskosTypesEndMarker,
		"}",
		"",
		BoskosResourcesBeginMarker,
		"print('old block')",
		BoskosResourcesEndMarker,
		"",
	}, "\n")
	if err := os.WriteFile(GenerateBoskosPythonPath(releaseRepo), []byte(generateBoskosContent), 0o644); err != nil {
		t.Fatalf("expected generate-boskos write to succeed: %v", err)
	}

	if err := RewriteGenerateBoskos(releaseRepo, catalog); err != nil {
		t.Fatalf("expected generate-boskos rewrite to succeed: %v", err)
	}

	rewrittenData, err := os.ReadFile(GenerateBoskosPythonPath(releaseRepo))
	if err != nil {
		t.Fatalf("expected rewritten generate-boskos read to succeed: %v", err)
	}
	rewritten := string(rewrittenData)
	for _, expected := range []string{
		"aro-hcp-dev-westus3-slot",
		"aro-hcp-int-uksouth-slot",
		"aro-hcp-prod-uksouth-slot",
	} {
		if !strings.Contains(rewritten, expected) {
			t.Fatalf("expected rewritten generate-boskos to contain %q, got %q", expected, rewritten)
		}
	}

	expectedResources, err := ExpectedBoskosResources(catalog)
	if err != nil {
		t.Fatalf("expected Boskos resources generation to succeed: %v", err)
	}

	resourceList := []BoskosResource{
		{Type: "some-other-resource", MinCount: intPtr(1), MaxCount: intPtr(1)},
		{Type: "aro-hcp-dev-quota-slice", MinCount: intPtr(1), MaxCount: intPtr(15)},
		{
			Type:  "aro-hcp-msi-mock-cs-sp-dev",
			Names: []string{"aro-hcp-msi-mock-cs-sp-dev-0", "aro-hcp-msi-mock-cs-sp-dev-1"},
		},
	}
	for resourceType, names := range expectedResources {
		resourceList = append(resourceList, BoskosResource{
			Type:  resourceType,
			Names: names,
		})
	}

	boskosData, err := yaml.Marshal(BoskosConfig{Resources: resourceList})
	if err != nil {
		t.Fatalf("expected Boskos yaml marshal to succeed: %v", err)
	}
	if err := os.WriteFile(BoskosYAMLPath(releaseRepo), boskosData, 0o644); err != nil {
		t.Fatalf("expected Boskos yaml write to succeed: %v", err)
	}

	if err := ValidateBoskosConfig(releaseRepo, catalog); err != nil {
		t.Fatalf("expected Boskos validation to succeed: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}
