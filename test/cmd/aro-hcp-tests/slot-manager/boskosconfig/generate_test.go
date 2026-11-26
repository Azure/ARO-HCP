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

package boskosconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
)

func TestRewriteGenerateBoskosAndValidateBoskosConfig(t *testing.T) {
	t.Parallel()

	slotCatalog := &catalog.Catalog{
		Version: 2,
		Environments: map[string]catalog.Environment{
			"dev": {
				DeploymentEnvironment: catalog.DeploymentEnvironment{Name: "ci01"},
				Pools: []catalog.Pool{
					{
						Name:          "dev",
						Subscriptions: catalog.PoolSubscriptions{E2E: "dev"},
						Region:        "westus3",
						ResourceType:  "aro-hcp-dev-westus3-slot",
						SlotCount:     1,
						SlotAssets:    catalog.SlotAssets{E2EIdentities: &catalog.E2EIdentitiesAsset{Allocation: catalog.AllocationDedicated, ResourceGroupPrefix: "aro-hcp-msi-container-dev", ResourceGroupCount: 20}},
					},
				},
			},
			"int": {
				DeploymentEnvironment: catalog.DeploymentEnvironment{Name: "int"},
				Pools: []catalog.Pool{
					{
						Name:          "int",
						Subscriptions: catalog.PoolSubscriptions{E2E: "int"},
						Region:        "uksouth",
						ResourceType:  "aro-hcp-int-uksouth-slot",
						SlotCount:     1,
						SlotAssets:    catalog.SlotAssets{E2EIdentities: &catalog.E2EIdentitiesAsset{Allocation: catalog.AllocationDedicated, ResourceGroupPrefix: "aro-hcp-msi-container-int", ResourceGroupCount: 20}},
					},
				},
			},
			"prod": {
				DeploymentEnvironment: catalog.DeploymentEnvironment{Name: "prod"},
				Pools: []catalog.Pool{
					{
						Name:          "prod",
						Subscriptions: catalog.PoolSubscriptions{E2E: "prod"},
						Region:        "uksouth",
						ResourceType:  "aro-hcp-prod-uksouth-slot",
						SlotCount:     2,
						SlotAssets:    catalog.SlotAssets{E2EIdentities: &catalog.E2EIdentitiesAsset{Allocation: catalog.AllocationDedicated, ResourceGroupPrefix: "aro-hcp-msi-container-prod", ResourceGroupCount: 15}},
					},
				},
			},
		},
	}
	if err := slotCatalog.Validate(); err != nil {
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

	if err := RewriteGenerateBoskos(releaseRepo, slotCatalog); err != nil {
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

	expectedResources, err := ExpectedBoskosResources(slotCatalog)
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

	if err := ValidateBoskosConfig(releaseRepo, slotCatalog); err != nil {
		t.Fatalf("expected Boskos validation to succeed: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}

func TestInvalidInventoryDoesNotRenderOrRewriteBoskos(t *testing.T) {
	t.Parallel()
	cat := loadCatalogFromYAML(t, independentCatalog)
	cat.AssetPools = nil

	rendered, err := RenderBoskosResourcesBlock(cat)
	if err == nil || !strings.Contains(err.Error(), `missing asset pool "bundles"`) {
		t.Fatalf("expected missing inventory error, got %v", err)
	}
	if rendered != "" {
		t.Fatalf("failed render returned partial generator: %q", rendered)
	}

	repo := t.TempDir()
	path := GenerateBoskosPythonPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := strings.Join([]string{
		BoskosTypesBeginMarker, "    'old-type': {},", BoskosTypesEndMarker,
		BoskosResourcesBeginMarker, "print('old resources')", BoskosResourcesEndMarker,
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RewriteGenerateBoskos(repo, cat); err == nil || !strings.Contains(err.Error(), `missing asset pool "bundles"`) {
		t.Fatalf("expected missing inventory error on rewrite, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("failed rewrite mutated the generator: %q", after)
	}
}

const independentCatalog = `version: 2
asset_pools:
- name: bundles
  kind: infrastructure_identities
  boskos_resource_type: infra-bundles
  resource_name_prefix: bundle
environments:
  dev:
    deployment_environment: {name: ci01, infrastructure_subscription: infra}
    pools:
    - name: shard0
      region: westus3
      slot_count: 2
      subscriptions: {e2e: customer}
      slot_assets:
        e2e_identities: {allocation: dedicated, resource_group_prefix: identities, resource_group_count: 1}
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
          units_per_slot: 2
  other:
    deployment_environment: {name: other-ci, infrastructure_subscription: infra}
    pools:
    - name: shard1
      region: centralus
      slot_count: 3
      subscriptions: {e2e: other-customer}
      slot_assets:
        e2e_identities: {allocation: dedicated, resource_group_prefix: other-identities, resource_group_count: 1}
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
`

func loadCatalogFromYAML(t *testing.T, catalogYAML string) *catalog.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "e2e-slots.yaml")
	if err := os.WriteFile(path, []byte(catalogYAML), 0o644); err != nil {
		t.Fatalf("expected synthetic catalog write to succeed: %v", err)
	}
	cat, err := catalog.LoadCatalog(path)
	if err != nil {
		t.Fatalf("expected synthetic catalog to load: %v", err)
	}
	return cat
}

func TestBoskosIncludesIndependentInventoryOnce(t *testing.T) {
	t.Parallel()
	cat := loadCatalogFromYAML(t, independentCatalog)
	expected, err := ExpectedBoskosResources(cat)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bundle-00", "bundle-01", "bundle-02", "bundle-03", "bundle-04", "bundle-05", "bundle-06"}
	if len(expected) != 3 || !reflect.DeepEqual(expected["infra-bundles"], want) {
		t.Fatalf("incorrect primary and secondary inventory: %v", expected)
	}
	if got := RenderBoskosTypesBlock(cat); strings.Count(got, "'infra-bundles':") != 1 {
		t.Fatalf("secondary type must occur once across environments: %s", got)
	}
	got, err := RenderBoskosResourcesBlock(cat)
	if err != nil {
		t.Fatalf("rendering aggregated generator: %v", err)
	}
	if strings.Count(got, "range(7)") != 1 || !strings.Contains(got, "['bundle-{i:0>2}'") {
		t.Fatalf("incorrect aggregated generator: %s", got)
	}
	repo := t.TempDir()
	config := BoskosConfig{}
	for kind, names := range expected {
		config.Resources = append(config.Resources, BoskosResource{Type: kind, Names: names})
	}
	write := func() {
		t.Helper()
		data, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		path := BoskosYAMLPath(repo)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := ValidateBoskosConfig(repo, cat); err != nil {
		t.Fatalf("valid combined inventory: %v", err)
	}
	for i := range config.Resources {
		if config.Resources[i].Type == "infra-bundles" {
			config.Resources[i].Names = want[:6]
		}
	}
	write()
	if err := ValidateBoskosConfig(repo, cat); err == nil {
		t.Fatal("under-capacity secondary inventory was accepted")
	}
}
