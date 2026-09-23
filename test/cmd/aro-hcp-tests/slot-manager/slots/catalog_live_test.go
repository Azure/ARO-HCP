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
	"reflect"
	"slices"
	"testing"
)

func TestLiveCatalogDedicatedIdentityInventory(t *testing.T) {
	t.Parallel()
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 2 || len(catalog.AssetPools) != 0 {
		t.Fatal("live catalog must use v2 without independent asset pools")
	}
	deployEnvs := map[string]string{"dev": "ci01", "int": "int", "stg": "stg", "prod": "prod"}
	if len(catalog.Environments) != len(deployEnvs) {
		t.Fatalf("unexpected environments: %v", catalog.EnvironmentNames())
	}
	if _, err := catalog.ResolveEnvironmentForDeployEnv("ci00"); err == nil {
		t.Fatal("ci00 must not resolve to a slot-manager environment")
	}

	// Pin the pre-migration operational inventory. Deliberate inventory changes
	// must update these expectations together with the live catalog.
	legacy := &Catalog{Version: 1, Environments: map[string]Environment{}}
	for _, expected := range []struct {
		environment, subscription, resourceType, prefix string
		slotCount, containerCount                       int
		mode                                            RegionMode
		region                                          string
		unmanaged                                       bool
	}{
		{"dev", "ARO HCP E2E Hosted Clusters (EA Subscription)", "aro-hcp-dev-shard0-slot", "aro-hcp-msi-container-dev-shard0", 5, 60, RegionModeWeighted, "", false},
		{"dev", "ARO HCP E2E Hosted Clusters 2 (EA Subscription)", "aro-hcp-dev-shard1-slot", "aro-hcp-msi-container-dev-shard1", 5, 60, RegionModeWeighted, "", false},
		{"dev", "ARO HCP E2E Hosted Clusters - Dev - 02", "aro-hcp-dev-shard2-slot", "aro-hcp-msi-container-dev-shard2", 5, 60, RegionModeWeighted, "", false},
		{"dev", "ARO HCP E2E Hosted Clusters - Dev - 03", "aro-hcp-dev-shard3-slot", "aro-hcp-msi-container-dev-shard3", 5, 60, RegionModeWeighted, "", false},
		{"dev", "Hypershift Managed Azure", "aro-hcp-dev-hypershift-westus3-slot", "aro-hcp-msi-container-dev", 1, 20, RegionModeWeighted, "", true},
		{"int", "ARO SRE Team - INT (EA Subscription 3)", "aro-hcp-int-shard0-slot", "aro-hcp-msi-container-int-shard0", 1, 20, RegionModeFixed, "uksouth", false},
		{"int", "ARO SRE Team - INT (EA Subscription 3)", "aro-hcp-int-westus3-shard0-slot", "aro-hcp-msi-container-int-westus3-shard0", 1, 20, RegionModeFixed, "westus3", false},
		{"stg", "ARO HCP E2E Hosted Clusters - Stage - 00", "aro-hcp-stg-shard0-slot", "aro-hcp-msi-container-stg-shard0", 1, 25, RegionModeRuntimeSelected, "uksouth", false},
		{"prod", "ARO HCP E2E Hosted Clusters - Prod - 00", "aro-hcp-prod-shard0-slot", "aro-hcp-msi-container-prod", 3, 25, RegionModeRuntimeSelected, "uksouth", false},
		{"prod", "ARO HCP E2E Hosted Clusters - Prod - 01", "aro-hcp-prod-shard1-slot", "aro-hcp-msi-container-prod", 3, 25, RegionModeRuntimeSelected, "uksouth", false},
		{"prod", "ARO HCP E2E", "aro-hcp-prod-testtenant-slot", "aro-hcp-msi-container-prod-testtenant", 4, 25, RegionModeRuntimeSelected, "uksouth", true},
	} {
		pool := Pool{
			SubscriptionName: expected.subscription, ResourceType: expected.resourceType,
			SlotCount: expected.slotCount, IdentityContainerPrefix: expected.prefix,
			IdentityContainerCount: expected.containerCount, RegionMode: expected.mode, Region: expected.region,
		}
		if expected.mode == RegionModeWeighted {
			pool.Regions = []string{"westus3", "centralus", "canadacentral"}
			pool.IdentityProvisioningRegion = "westus3"
		}
		if expected.unmanaged {
			pool.IdentityProvisioning = IdentityProvisioningUnmanaged
		}
		environment := legacy.Environments[expected.environment]
		environment.DeployEnvs = []string{deployEnvs[expected.environment]}
		environment.Pools = append(environment.Pools, pool)
		legacy.Environments[expected.environment] = environment
	}
	if err := legacy.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, deployEnv := range deployEnvs {
		t.Run(name, func(t *testing.T) {
			environment := catalog.Environments[name]
			if environment.DeploymentEnvironment != (DeploymentEnvironment{Name: deployEnv}) {
				t.Fatalf("unexpected deployment or infrastructure binding: %+v", environment.DeploymentEnvironment)
			}
			if resolved, err := catalog.ResolveEnvironmentForDeployEnv(deployEnv); err != nil || resolved != name {
				t.Fatalf("existing deploy selector no longer resolves: %q, %v", resolved, err)
			}
			wantPools := legacy.Environments[name].Pools
			if len(environment.Pools) != len(wantPools) {
				t.Fatalf("pool count changed: got %d, want %d", len(environment.Pools), len(wantPools))
			}
			for index, pool := range environment.Pools {
				wantPool := wantPools[index]
				wantSlots := ExpandSlotsForPool(name, wantPool)
				gotSlots := ExpandSlotsForPool(name, pool)
				if len(gotSlots) != len(wantSlots) {
					t.Fatalf("slot count changed for %s: got %d, want %d", pool.ResourceType, len(gotSlots), len(wantSlots))
				}
				for slotIndex, slot := range gotSlots {
					if slot.RequiresInfrastructureSubscription() || slot.Subscriptions.Infrastructure != (ResolvedSubscription{}) {
						t.Fatalf("%s unexpectedly requires infrastructure", slot.ResourceName)
					}
					if slot.Subscriptions.E2E.Name != wantPool.SubscriptionName ||
						slot.DeployEnvironment != deployEnv || slot.PoolName == "" {
						t.Fatalf("incomplete v2 bindings for %s", slot.ResourceName)
					}
					asset := slot.Assets.E2EIdentities
					if asset == nil || asset.Allocation != AllocationDedicated ||
						asset.ProvisioningRegion != wantPool.EffectiveIdentityProvisioningRegion() ||
						!slices.Equal(asset.ResourceGroups, wantSlots[slotIndex].IdentityContainerNames()) {
						t.Fatalf("dedicated identity asset changed for %s: %+v", slot.ResourceName, asset)
					}
					slot.PoolName, slot.DeployEnvironment = "", ""
					slot.Subscriptions, slot.Assets = ResolvedSubscriptions{}, ResolvedAssets{}
					if !reflect.DeepEqual(slot, wantSlots[slotIndex]) {
						t.Fatalf("expanded slot changed:\ngot  %+v\nwant %+v", slot, wantSlots[slotIndex])
					}
				}
				pool.Name, pool.DeployEnv = "", ""
				pool.Subscriptions, pool.SlotAssets = PoolSubscriptions{}, SlotAssets{}
				if !reflect.DeepEqual(pool, wantPool) {
					t.Fatalf("pool inventory or runtime policy changed:\ngot  %+v\nwant %+v", pool, wantPool)
				}
			}
		})
	}
	gotResources, err := ExpectedBoskosResources(catalog)
	if err != nil {
		t.Fatal(err)
	}
	wantResources, err := ExpectedBoskosResources(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotResources, wantResources) {
		t.Fatalf("Boskos inventory changed:\ngot  %v\nwant %v", gotResources, wantResources)
	}
}
