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
	"strings"
	"testing"
)

func TestMockIdentityCatalog(t *testing.T) {
	input := strings.Replace(syntheticCatalogYAML, "        slot_assets:\n", "        slot_assets:\n          mock_identities:\n            allocation: dedicated\n", 1)
	catalog, err := loadCatalogFromYAMLWithError(t, input)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := catalog.ExpandedSlotsForEnvironment("dev")
	if err != nil {
		t.Fatal(err)
	}
	if slots[0].MockIdentityConfig == nil || slots[0].MockIdentityConfig.Allocation != AllocationDedicated {
		t.Fatal("mock identity declaration lost during expansion")
	}
	if err := slots[0].ValidateResolvedAssets(); err == nil {
		t.Fatal("unresolved mock bundle accepted")
	}
	for _, allocation := range []string{"leased", "unknown"} {
		invalid := strings.Replace(input, "mock_identities:\n            allocation: dedicated", "mock_identities:\n            allocation: "+allocation, 1)
		if _, err := loadCatalogFromYAMLWithError(t, invalid); err == nil {
			t.Fatal("accepted unsupported allocation")
		}
	}
	canonical, err := LoadCatalog("../../../../e2e-config/e2e-slots.yaml")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, environment := range []string{"dev", "dev-xs"} {
		for _, pool := range canonical.Environments[environment].Pools {
			if pool.SlotAssets.MockIdentities == nil {
				t.Fatalf("%s/%s lacks mock asset", environment, pool.Name)
			}
			total += pool.SlotCount
		}
	}
	if total != 112 {
		t.Fatalf("changed declared DEV capacity: %d", total)
	}
}
