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

package identitypool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func fakeResolver(ids map[string]string) subscriptionIDResolverFunc {
	return func(_ context.Context, name string) (string, error) {
		id, ok := ids[name]
		if !ok {
			return "", fmt.Errorf("unknown subscription %q", name)
		}
		return id, nil
	}
}

func TestLoadIdentityPools(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	catalog := `version: 1
environments:
  dev:
    deploy_envs:
      - ci00
    pools:
      - subscription_name: dev-sub-1
        region: westus3
        region_mode: runtime-selected
        identity_provisioning_region: centralus
        resource_type: aro-hcp-dev-westus3-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
      - subscription_name: dev-sub-2
        region: eastus2
        region_mode: runtime-selected
        resource_type: aro-hcp-dev-eastus2-slot
        slot_count: 2
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 1
`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}

	resolver := fakeResolver(map[string]string{
		"dev-sub-1": "00000000-0000-0000-0000-000000000001",
		"dev-sub-2": "00000000-0000-0000-0000-000000000002",
	})

	pools, err := loadIdentityPools(context.Background(), catalogPath, "dev", nil, resolver)
	if err != nil {
		t.Fatalf("expected identity pools to load: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	if pools[0].SubscriptionName != "dev-sub-1" || pools[0].Region != "westus3" || pools[0].ProvisioningRegion != "centralus" {
		t.Fatalf("unexpected first pool: %+v", pools[0])
	}
	if pools[0].SubscriptionID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected subscription ID to be resolved for first pool, got %q", pools[0].SubscriptionID)
	}
	if len(pools[0].Slots) != 1 || pools[0].Slots[0].ResourceName != "aro-hcp-dev-westus3-slot-00" {
		t.Fatalf("unexpected first pool slots: %+v", pools[0].Slots)
	}

	if pools[1].SubscriptionName != "dev-sub-2" || pools[1].Region != "eastus2" || pools[1].ProvisioningRegion != "eastus2" {
		t.Fatalf("unexpected second pool: %+v", pools[1])
	}
	if pools[1].SubscriptionID != "00000000-0000-0000-0000-000000000002" {
		t.Fatalf("expected subscription ID to be resolved for second pool, got %q", pools[1].SubscriptionID)
	}
	if len(pools[1].Slots) != 2 || pools[1].Slots[1].ResourceName != "aro-hcp-dev-eastus2-slot-01" {
		t.Fatalf("unexpected second pool slots: %+v", pools[1].Slots)
	}
}

func TestLoadIdentityPoolsResolutionFailure(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	catalog := `version: 1
environments:
  dev:
    deploy_envs:
      - ci00
    pools:
      - subscription_name: unknown-sub
        region: westus3
        region_mode: runtime-selected
        resource_type: aro-hcp-dev-westus3-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 1
`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}

	resolver := fakeResolver(map[string]string{})

	_, err := loadIdentityPools(context.Background(), catalogPath, "dev", nil, resolver)
	if err == nil {
		t.Fatal("expected error when subscription resolution fails")
	}
}

func TestLoadIdentityPoolsDeduplicatesResolution(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	catalog := `version: 1
environments:
  dev:
    deploy_envs:
      - ci00
    pools:
      - subscription_name: shared-sub
        region: westus3
        region_mode: fixed
        resource_type: aro-hcp-dev-westus3-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 1
      - subscription_name: shared-sub
        region: eastus2
        region_mode: fixed
        resource_type: aro-hcp-dev-eastus2-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 1
`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}

	calls := 0
	resolver := func(_ context.Context, name string) (string, error) {
		calls++
		if name == "shared-sub" {
			return "00000000-0000-0000-0000-000000000099", nil
		}
		return "", fmt.Errorf("unknown subscription %q", name)
	}

	pools, err := loadIdentityPools(context.Background(), catalogPath, "dev", nil, resolver)
	if err != nil {
		t.Fatalf("expected identity pools to load: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected resolver to be called once for deduplicated subscription, got %d calls", calls)
	}
	for i, p := range pools {
		if p.SubscriptionID != "00000000-0000-0000-0000-000000000099" {
			t.Fatalf("pool[%d]: expected resolved subscription ID, got %q", i, p.SubscriptionID)
		}
	}
}
