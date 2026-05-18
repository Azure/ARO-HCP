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
	"testing"
)

const syntheticCatalogYAML = `version: 1
environments:
  dev:
    deploy_envs: [prow, ci01]
    pools:
      - subscription_name: dev-shard-0
        region: westus3
        resource_type: aro-hcp-dev-shard0-westus3-slot
        slot_count: 2
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 20
      - subscription_name: dev-shard-1
        region: eastus2
        resource_type: aro-hcp-dev-shard1-eastus2-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 10
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod
        region: uksouth
        resource_type: aro-hcp-prod-uksouth-slot
        slot_count: 3
        identity_container_prefix: aro-hcp-msi-container-prod
        identity_container_count: 15
`

func loadCatalogFromYAML(t *testing.T, catalogYAML string) *Catalog {
	t.Helper()

	catalogPath := filepath.Join(t.TempDir(), "e2e-slots.yaml")
	if err := os.WriteFile(catalogPath, []byte(catalogYAML), 0o644); err != nil {
		t.Fatalf("expected synthetic catalog write to succeed: %v", err)
	}

	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		t.Fatalf("expected synthetic catalog to load: %v", err)
	}

	return catalog
}

func loadSyntheticCatalog(t *testing.T) *Catalog {
	t.Helper()
	return loadCatalogFromYAML(t, syntheticCatalogYAML)
}

func TestLoadCatalogAndExpandSlots(t *testing.T) {
	t.Parallel()

	catalog := loadSyntheticCatalog(t)

	devSlots, err := catalog.ExpandedSlotsForEnvironment("dev")
	if err != nil {
		t.Fatalf("expected dev slots to expand: %v", err)
	}
	if len(devSlots) != 3 {
		t.Fatalf("expected 3 dev slots, got %d", len(devSlots))
	}

	firstDevSlot := devSlots[0]
	expectedSubscriptionName := catalog.Environments["dev"].Pools[0].SubscriptionName
	if firstDevSlot.ResourceType != "aro-hcp-dev-shard0-westus3-slot" {
		t.Fatalf("unexpected resource type %q", firstDevSlot.ResourceType)
	}
	if firstDevSlot.ResourceName != "aro-hcp-dev-shard0-westus3-slot-00" {
		t.Fatalf("unexpected resource name %q", firstDevSlot.ResourceName)
	}
	if firstDevSlot.SubscriptionName != expectedSubscriptionName {
		t.Fatalf("unexpected subscription name %q", firstDevSlot.SubscriptionName)
	}
	if firstDevSlot.IdentityContainerPrefix != "aro-hcp-msi-container-dev-a-00" {
		t.Fatalf("unexpected identity container prefix %q", firstDevSlot.IdentityContainerPrefix)
	}

	identityContainers := firstDevSlot.IdentityContainerNames()
	if len(identityContainers) != 20 {
		t.Fatalf("expected 20 identity containers, got %d", len(identityContainers))
	}
	if identityContainers[0] != "aro-hcp-msi-container-dev-a-00-00" {
		t.Fatalf("unexpected first identity container %q", identityContainers[0])
	}
	if identityContainers[19] != "aro-hcp-msi-container-dev-a-00-19" {
		t.Fatalf("unexpected last identity container %q", identityContainers[19])
	}
}

func TestResolvePool(t *testing.T) {
	t.Parallel()

	catalog := loadSyntheticCatalog(t)

	pool, err := catalog.ResolvePool("dev", "dev-shard-0", "westus3")
	if err != nil {
		t.Fatalf("expected pool resolution to succeed: %v", err)
	}
	if pool.ResourceType != "aro-hcp-dev-shard0-westus3-slot" {
		t.Fatalf("unexpected resource type %q", pool.ResourceType)
	}
}

func TestLoadCatalogDefaultsRegionModeToFixed(t *testing.T) {
	t.Parallel()

	catalog := loadSyntheticCatalog(t)

	if got := catalog.Environments["dev"].Pools[0].RegionMode; got != RegionModeFixed {
		t.Fatalf("expected default region_mode %q, got %q", RegionModeFixed, got)
	}
}

func TestLoadCatalogRejectsMixedRegionModesWithinEnvironment(t *testing.T) {
	t.Parallel()

	invalidCatalog := `version: 1
environments:
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod-sub-1
        region: uksouth
        region_mode: fixed
        resource_type: aro-hcp-prod-uksouth-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-a
        identity_container_count: 1
      - subscription_name: prod-sub-2
        region: eastus2
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard1-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-b
        identity_container_count: 1
`

	catalogPath := filepath.Join(t.TempDir(), "e2e-slots.yaml")
	if err := os.WriteFile(catalogPath, []byte(invalidCatalog), 0o644); err != nil {
		t.Fatalf("expected invalid catalog write to succeed: %v", err)
	}

	if _, err := LoadCatalog(catalogPath); err == nil {
		t.Fatal("expected mixed region_mode catalog load to fail")
	}
}

func TestLoadCatalogRejectsDuplicateRuntimeSelectedSubscriptionPools(t *testing.T) {
	t.Parallel()

	invalidCatalog := `version: 1
environments:
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod-sub
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard0-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-a
        identity_container_count: 1
      - subscription_name: prod-sub
        region: eastus2
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard1-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-b
        identity_container_count: 1
`

	catalogPath := filepath.Join(t.TempDir(), "e2e-slots.yaml")
	if err := os.WriteFile(catalogPath, []byte(invalidCatalog), 0o644); err != nil {
		t.Fatalf("expected invalid catalog write to succeed: %v", err)
	}

	if _, err := LoadCatalog(catalogPath); err == nil {
		t.Fatal("expected duplicate runtime-selected subscription pools to fail validation")
	}
}

func TestResolvePoolRuntimeSelectedIgnoresRegion(t *testing.T) {
	t.Parallel()

	catalog := loadCatalogFromYAML(t, `version: 1
environments:
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod-sub
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard0-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod
        identity_container_count: 1
`)

	pool, err := catalog.ResolvePool("prod", "", "eastus2")
	if err != nil {
		t.Fatalf("expected runtime-selected pool resolution to ignore region: %v", err)
	}
	if pool.ResourceType != "aro-hcp-prod-shard0-slot" {
		t.Fatalf("unexpected resource type %q", pool.ResourceType)
	}
}

func TestResolvePoolRuntimeSelectedRequiresSubscriptionToDisambiguate(t *testing.T) {
	t.Parallel()

	catalog := loadCatalogFromYAML(t, `version: 1
environments:
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod-sub-1
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard0-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-a
        identity_container_count: 1
      - subscription_name: prod-sub-2
        region: westus3
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard1-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-b
        identity_container_count: 1
`)

	_, err := catalog.ResolvePool("prod", "", "eastus2")
	if err == nil {
		t.Fatal("expected runtime-selected pool resolution to require subscription selector when multiple pools exist")
	}
}

func TestFindSlotByResourceName(t *testing.T) {
	t.Parallel()

	catalog := loadSyntheticCatalog(t)

	slot, err := catalog.FindSlotByResourceName("aro-hcp-prod-uksouth-slot-02")
	if err != nil {
		t.Fatalf("expected slot lookup to succeed: %v", err)
	}
	if slot.Environment != "prod" {
		t.Fatalf("expected prod environment, got %q", slot.Environment)
	}
	if slot.IdentityContainerPrefix != "aro-hcp-msi-container-prod-02" {
		t.Fatalf("unexpected identity container prefix %q", slot.IdentityContainerPrefix)
	}
}

func TestResolveEnvironmentForDeployEnv(t *testing.T) {
	t.Parallel()

	catalog := loadSyntheticCatalog(t)

	environment, err := catalog.ResolveEnvironmentForDeployEnv("ci01")
	if err != nil {
		t.Fatalf("expected deploy environment resolution to succeed: %v", err)
	}
	if environment != "dev" {
		t.Fatalf("expected dev environment, got %q", environment)
	}
}

func TestResolveCatalogPath(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("expected working directory: %v", err)
	}

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	nestedDir := filepath.Join(repoRoot, "test", "cmd", "aro-hcp-tests", "slot-manager", "slots")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("expected nested directory creation to succeed: %v", err)
	}

	catalogDir := filepath.Join(repoRoot, "test", "e2e-config")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("expected catalog directory creation to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "e2e-slots.yaml"), []byte("version: 1\nenvironments:\n  dev:\n    deploy_envs: [dev]\n    pools:\n      - subscription_name: dev\n        region: westus3\n        resource_type: type\n        slot_count: 1\n        identity_container_prefix: prefix\n        identity_container_count: 1\n"), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}

	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("expected chdir to nested directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(workingDir)
	})

	resolvedPath, err := ResolveCatalogPath()
	if err != nil {
		t.Fatalf("expected catalog resolution to succeed: %v", err)
	}
	expectedPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	if resolvedPath != expectedPath {
		t.Fatalf("expected %q, got %q", expectedPath, resolvedPath)
	}
}

func TestSharedStatePaths(t *testing.T) {
	t.Parallel()

	stateDir, err := SharedStateDir("/tmp/shared")
	if err != nil {
		t.Fatalf("expected state dir to resolve: %v", err)
	}
	if stateDir != "/tmp/shared" {
		t.Fatalf("unexpected state dir %q", stateDir)
	}

	envFile, err := EnvFile("/tmp/shared")
	if err != nil {
		t.Fatalf("expected env file to resolve: %v", err)
	}
	if envFile != "/tmp/shared/aro-hcp-slot-env.sh" {
		t.Fatalf("unexpected env file %q", envFile)
	}
}
