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
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
`

func TestAssetInventoryContainsCanonicalNames(t *testing.T) {
	t.Parallel()
	inventory := AssetInventory{Pool: AssetPool{ResourceNamePrefix: "bundle"}, Capacity: 101}
	for _, name := range []string{"bundle-00", "bundle-01", "bundle-99", "bundle-100"} {
		if !inventory.Contains(name) {
			t.Errorf("rejected canonical name %q", name)
		}
	}
	for _, name := range []string{
		"bundle-101", "bundle-1", "bundle-001", "bundle-0100", "bundle-+1", "bundle-+01",
		"bundle--1", "bundle- 1", "bundle-01 ", "bundle-01\n", "bundle-1suffix", "bundle-0x01",
		"bundle-1.0", "bundle-99999999999999999999999999", "bundle-", "foreign-01", "01",
	} {
		if inventory.Contains(name) {
			t.Errorf("accepted noncanonical or out-of-range name %q", name)
		}
	}
	inventory.Capacity = 0
	if inventory.Contains("bundle-00") {
		t.Fatal("empty inventory accepted a resource")
	}
}

func TestAssetInventoryAggregatesWholeCatalog(t *testing.T) {
	t.Parallel()
	catalog := loadCatalogFromYAML(t, independentCatalog)
	pools, err := catalog.CandidatePools("dev", nil, nil, "")
	if err != nil || len(pools) != 1 {
		t.Fatalf("selecting one consumer: %v, %v", pools, err)
	}
	inventories, err := catalog.AssetInventories()
	if err != nil || len(inventories) != 1 || inventories[0].Capacity != 7 {
		t.Fatalf("whole catalog demand must be 2*2 + 3*1: %+v, %v", inventories, err)
	}
	if inventories[0].SubscriptionName != "infra" || inventories[0].Pool.Provisioning != AssetProvisioningManaged {
		t.Fatalf("incorrect normalized inventory: %+v", inventories[0])
	}
	slot := ExpandSlotsForPool("dev", pools[0])[0]
	if slot.DeployEnvironment != "ci01" || slot.Subscriptions.Infrastructure.Name != "infra" || slot.Assets.E2EIdentities != nil {
		t.Fatalf("binding or absent asset handling is incorrect: %+v", slot)
	}
}

func TestV2RejectsObsoleteAndInvalidCatalogs(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, from, to, want string }{
		{"pool deploy env", "      region: westus3", "      deploy_env: prod\n      region: westus3", "deploy_env"},
		{"pool infrastructure", "{e2e: customer}", "{e2e: customer, infrastructure: infra}", "infrastructure"},
		{"legacy subscription", "      region: westus3", "      subscription_name: customer\n      region: westus3", "obsolete"},
		{"legacy identity count", "      region: westus3", "      identity_container_count: 1\n      region: westus3", "obsolete"},
		{"plural binding", "    deployment_environment: {name: ci01, infrastructure_subscription: infra}", "    deploy_envs: [ci01]", "deploy_envs"},
		{"missing binding", "    deployment_environment: {name: ci01, infrastructure_subscription: infra}", "", "deployment_environment"},
		{"missing deployment name", "name: ci01, infrastructure_subscription: infra", "infrastructure_subscription: infra", "deployment_environment.name"},
		{"missing E2E subscription", "subscriptions: {e2e: customer}", "subscriptions: {}", "subscriptions.e2e"},
		{"missing demanded infrastructure binding", "name: ci01, infrastructure_subscription: infra", "name: ci01", "deployment_environment.infrastructure_subscription"},
		{"blank demanded infrastructure binding", "name: ci01, infrastructure_subscription: infra", "name: ci01, infrastructure_subscription: '  '", "deployment_environment.infrastructure_subscription"},
		{"deployment path traversal", "name: ci01,", "name: ../../outside,", "invalid deployment environment name"},
		{"deployment backslash traversal", "name: ci01,", `name: '..\..\outside',`, "invalid deployment environment name"},
		{"deployment path separator", "name: ci01,", "name: dev/ci01,", "invalid deployment environment name"},
		{"unknown field", "      region: westus3", "      mystery: true\n      region: westus3", "mystery"},
		{"unknown asset declaration", "        infrastructure_identities:", "        imaginary_identities:", "imaginary_identities"},
		{"missing asset pool", "asset_pool: bundles", "asset_pool: missing", "missing asset pool"},
		{"unused asset pool", "        infrastructure_identities:\n          allocation: leased\n          asset_pool: bundles\n", "", "unused"},
		{"unsupported kind", "kind: infrastructure_identities", "kind: imaginary", "kind"},
		{"mismatched kind", "kind: infrastructure_identities", "kind: e2e_identities", "kind"},
		{"incompatible subscriptions", "name: other-ci, infrastructure_subscription: infra", "name: other-ci, infrastructure_subscription: different", "incompatible"},
		{"secondary primary type collision", "boskos_resource_type: infra-bundles", "boskos_resource_type: aro-hcp-dev-shard0-slot", "resource type"},
		{"secondary primary name collision", "resource_name_prefix: bundle", "resource_name_prefix: aro-hcp-dev-shard0-slot", "duplicate resource names"},
		{"invalid provisioning", "  kind: infrastructure_identities", "  kind: infrastructure_identities\n  provisioning: sometimes", "provisioning"},
		{"zero units", "units_per_slot: 2", "units_per_slot: 0", "positive"},
		{"negative units", "units_per_slot: 2", "units_per_slot: -1", "positive"},
		{"shared allocation", "allocation: leased", "allocation: shared", "allocation"},
		{"ephemeral allocation", "allocation: leased", "allocation: ephemeral", "allocation"},
		{"unknown demand config", "units_per_slot: 2", "units_per_slot: 2\n          subscription: foreign", "subscription"},
		{"inventory capacity forbidden", "  resource_name_prefix: bundle", "  resource_name_prefix: bundle\n  capacity: 3", "capacity"},
		{"inventory subscription forbidden", "  resource_name_prefix: bundle", "  resource_name_prefix: bundle\n  subscription: infra", "subscription"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := strings.ReplaceAll(independentCatalog, test.from, test.to)
			if test.name == "unused asset pool" {
				input = strings.ReplaceAll(input, "          units_per_slot: 2\n", "")
			}
			_, err := loadCatalogFromYAMLWithError(t, input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q rejection, got %v", test.want, err)
			}
		})
	}
}

func TestV2MixedPoolsRequireInfrastructureOnlyForConsumers(t *testing.T) {
	t.Parallel()
	input := strings.Replace(independentCatalog, "    pools:\n", `    pools:
    - name: e2e-only
      region: westus3
      slot_count: 1
      subscriptions: {e2e: e2e-only}
`, 1)
	catalog := loadCatalogFromYAML(t, input)
	pools := catalog.Environments["dev"].Pools
	if pools[0].InfrastructureSubscriptionName() != "" || pools[0].Subscriptions.Infrastructure != "" || pools[1].InfrastructureSubscriptionName() != "infra" {
		t.Fatalf("infrastructure binding was not limited to consumers: %+v", pools)
	}
	inventories, err := catalog.AssetInventories()
	if err != nil || len(inventories) != 1 || inventories[0].Capacity != 7 || inventories[0].SubscriptionName != "infra" {
		t.Fatalf("E2E-only pool changed infrastructure inventory: %+v, %v", inventories, err)
	}
	_, err = loadCatalogFromYAMLWithError(t, strings.Replace(input, "name: ci01, infrastructure_subscription: infra", "name: ci01", 1))
	if err == nil || !strings.Contains(err.Error(), "deployment_environment.infrastructure_subscription") {
		t.Fatalf("mixed environment accepted missing infrastructure binding: %v", err)
	}
	environment := catalog.Environments["dev"]
	environment.DeploymentEnvironment.InfrastructureSubscription = ""
	catalog.Environments["dev"] = environment
	if _, err := catalog.AssetInventories(); err == nil || !strings.Contains(err.Error(), "deployment_environment.infrastructure_subscription") {
		t.Fatalf("inventory accepted unresolved subscription ownership: %v", err)
	}
}

func TestDedicatedIdentityPrefixesRemainDistinct(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		secondPrefix string
		subscription string
		wantError    bool
	}{
		{"slot suffix in prefix", "foo-00", "customer", false},
		{"both suffixes in prefix", "foo-00-00", "customer", false},
		{"identical prefix", "foo", "customer", true},
		{"case insensitive prefix", "FOO", "customer", true},
		{"different subscription", "foo", "other-customer", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := func(name, prefix, subscription string) Pool {
				return Pool{
					Name: name, Region: "westus3", SlotCount: 2,
					Subscriptions: PoolSubscriptions{E2E: subscription},
					SlotAssets: SlotAssets{E2EIdentities: &E2EIdentitiesAsset{
						Allocation: AllocationDedicated, ResourceGroupPrefix: prefix, ResourceGroupCount: 102,
					}},
				}
			}
			catalog := &Catalog{Version: 2, Environments: map[string]Environment{
				"dev": {
					DeploymentEnvironment: DeploymentEnvironment{Name: "ci01", InfrastructureSubscription: "infra"},
					Pools:                 []Pool{pool("first", "foo", "customer"), pool("second", test.secondPrefix, test.subscription)},
				},
			}}
			err := catalog.Validate()
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "incompatible dedicated resource groups") {
					t.Fatalf("expected duplicate identity prefix rejection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("distinct identity prefixes were rejected: %v", err)
			}
			groups := map[string]bool{}
			for _, pool := range catalog.Environments["dev"].Pools {
				expanded := ExpandSlotsForPool("dev", pool)
				wantFirst := pool.SlotAssets.E2EIdentities.ResourceGroupPrefix + "-00-00"
				if got := expanded[0].IdentityContainerNames()[0]; got != wantFirst {
					t.Fatalf("expected first group %q, got %q", wantFirst, got)
				}
				for _, slot := range expanded {
					for _, group := range slot.IdentityContainerNames() {
						key := strings.ToLower(slot.SubscriptionName + "/" + group)
						if groups[key] {
							t.Fatalf("expanded identity group collides: %s", key)
						}
						groups[key] = true
					}
				}
			}
			if len(groups) != 2*2*102 {
				t.Fatalf("unexpected distinct group count: %d", len(groups))
			}
		})
	}
}

func TestV2AssetPoolDuplicateAndOverflowValidation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"duplicate pool", "duplicate type", "duplicate name", "multiply overflow", "sum overflow"} {
		t.Run(scenario, func(t *testing.T) {
			catalog := loadCatalogFromYAML(t, independentCatalog)
			switch scenario {
			case "duplicate pool", "duplicate type", "duplicate name":
				extra := catalog.AssetPools[0]
				switch scenario {
				case "duplicate pool":
					extra.ResourceType, extra.ResourceNamePrefix = "extra-type", "extra-prefix"
				case "duplicate type":
					extra.Name, extra.ResourceNamePrefix = "extra-pool", "extra-prefix"
				case "duplicate name":
					extra.Name, extra.ResourceType = "extra-pool", "extra-type"
				}
				catalog.AssetPools = append(catalog.AssetPools, extra)
			case "multiply overflow":
				catalog.Environments["dev"].Pools[0].SlotCount = math.MaxInt
			case "sum overflow":
				catalog.Environments["dev"].Pools[0].SlotCount = math.MaxInt / 2
			}
			if err := catalog.Validate(); err == nil {
				t.Fatal("invalid inventory was accepted")
			}
		})
	}
}

func TestBoskosIncludesIndependentInventoryOnce(t *testing.T) {
	t.Parallel()
	catalog := loadCatalogFromYAML(t, independentCatalog)
	expected, err := ExpectedBoskosResources(catalog)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bundle-00", "bundle-01", "bundle-02", "bundle-03", "bundle-04", "bundle-05", "bundle-06"}
	if len(expected) != 3 || !reflect.DeepEqual(expected["infra-bundles"], want) {
		t.Fatalf("incorrect primary and secondary inventory: %v", expected)
	}
	if got := RenderBoskosTypesBlock(catalog); strings.Count(got, "'infra-bundles':") != 1 {
		t.Fatalf("secondary type must occur once across environments: %s", got)
	}
	got, err := RenderBoskosResourcesBlock(catalog)
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
	if err := ValidateBoskosConfig(repo, catalog); err != nil {
		t.Fatalf("valid combined inventory: %v", err)
	}
	for i := range config.Resources {
		if config.Resources[i].Type == "infra-bundles" {
			config.Resources[i].Names = want[:6]
		}
	}
	write()
	if err := ValidateBoskosConfig(repo, catalog); err == nil {
		t.Fatal("under-capacity secondary inventory was accepted")
	}
}
