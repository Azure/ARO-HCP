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

package compute

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
)

func TestFamilyAllocationOrder(t *testing.T) {
	// famFull and famFull2 are offered in all three zones; famRestricted in a
	// single zone; famNone in none. famMissing has no SKU at the tier's core
	// count. All eligible SKUs sit at 8 vCPUs to match the tiers below.
	index := EligibleSKUIndex{
		"famFull":       {8: {VMSize: "full8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"1", "2", "3"}}}},
		"famFull2":      {8: {VMSize: "full2-8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"1", "2", "3"}}}},
		"famRestricted": {8: {VMSize: "restricted8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"2"}}}},
		"famNone":       {8: {VMSize: "none8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: nil}}},
	}
	zones := []string{"1", "2", "3"}

	tests := []struct {
		name           string
		poolMode       PoolMode
		familyPriority []VMFamily
		want           []VMFamily
	}{
		{
			name:           "per-zone tier keeps the operator's priority",
			poolMode:       PoolModePerZone,
			familyPriority: []VMFamily{"famFull", "famRestricted"},
			want:           []VMFamily{"famFull", "famRestricted"},
		},
		{
			name:           "regional tier prefers the more zone-restricted family",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull"},
		},
		{
			name:           "regional tier sorts a zero-zone family last",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famNone", "famFull", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull", "famNone"},
		},
		{
			name:           "regional tier sorts a family without an eligible SKU last",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famMissing", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull", "famMissing"},
		},
		{
			name:           "regional tier keeps operator order on equal coverage",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famFull2"},
			want:           []VMFamily{"famFull", "famFull2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := TierConfig{
				Cores:          8,
				PoolMode:       tt.poolMode,
				FamilyPriority: tt.familyPriority,
			}
			got := familyAllocationOrder(tier, zones, index)
			assert.Equal(t, tt.want, got, "family allocation order mismatch")
		})
	}
}
