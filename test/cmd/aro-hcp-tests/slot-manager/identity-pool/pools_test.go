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
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIdentityPools(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	catalog := `version: 1
environments:
  dev:
    deploy_envs:
      - prow
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

	pools, err := loadIdentityPools(catalogPath, "dev")
	if err != nil {
		t.Fatalf("expected identity pools to load: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	if pools[0].SubscriptionName != "dev-sub-1" || pools[0].Region != "westus3" || pools[0].ProvisioningRegion != "centralus" {
		t.Fatalf("unexpected first pool: %+v", pools[0])
	}
	if len(pools[0].Slots) != 1 || pools[0].Slots[0].ResourceName != "aro-hcp-dev-westus3-slot-00" {
		t.Fatalf("unexpected first pool slots: %+v", pools[0].Slots)
	}

	if pools[1].SubscriptionName != "dev-sub-2" || pools[1].Region != "eastus2" || pools[1].ProvisioningRegion != "eastus2" {
		t.Fatalf("unexpected second pool: %+v", pools[1])
	}
	if len(pools[1].Slots) != 2 || pools[1].Slots[1].ResourceName != "aro-hcp-dev-eastus2-slot-01" {
		t.Fatalf("unexpected second pool slots: %+v", pools[1].Slots)
	}
}
