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

func TestBuildEligibleSKUIndex(t *testing.T) {
	tests := []struct {
		name         string
		skuMetadata  map[string]*skucache.SKUMetadata
		lookupFamily VMFamily
		lookupCores  int64
		wantVMSize   string
		wantFound    bool
	}{
		{
			name: "exact core match",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "standardEDSv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32ds_v6",
			wantFound:    true,
		},
		{
			name: "no exact core match",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "standardEDSv6Family",
			lookupCores:  16,
			wantFound:    false,
		},
		{
			name: "no ephemeral OS disk filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D8ds_v5": {VCPUs: 8, Family: "standardDDSv5Family", EphemeralOSDiskSupported: false, EphemeralDiskSizeGB: 320},
			},
			lookupFamily: "standardDDSv5Family",
			lookupCores:  8,
			wantFound:    false,
		},
		{
			name: "zero ephemeral disk size filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 0},
			},
			lookupFamily: "standardEDSv6Family",
			lookupCores:  32,
			wantFound:    false,
		},
		{
			name: "constrained vCPU SKU filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E16-4s_v3": {VCPUs: 16, Family: "standardESv3Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 256, ConstrainedVCPUs: true},
				"Standard_E16s_v3":   {VCPUs: 16, Family: "standardESv3Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 256},
			},
			lookupFamily: "standardESv3Family",
			lookupCores:  16,
			wantVMSize:   "Standard_E16s_v3",
			wantFound:    true,
		},
		{
			name: "zone restricted SKU is still indexed (zone eligibility is per-tier at allocation)",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792, Zones: []string{"1", "2"}},
			},
			lookupFamily: "standardEDSv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32ds_v6",
			wantFound:    true,
		},
		{
			name: "deterministic selection picks lexicographically smallest",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
				"Standard_E32as_v6": {VCPUs: 32, Family: "standardEDSv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "standardEDSv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32as_v6",
			wantFound:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := BuildEligibleSKUIndex(test.skuMetadata)
			vmSize, _, found := index.Lookup(test.lookupFamily, test.lookupCores)
			assert.Equal(t, test.wantFound, found)
			assert.Equal(t, test.wantVMSize, vmSize)
		})
	}
}
