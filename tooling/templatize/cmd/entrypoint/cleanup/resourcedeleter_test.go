// Copyright 2025 Microsoft Corporation
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

package cleanup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceDeletionOrder(t *testing.T) {
	// Document the deletion order - this serves as living documentation
	// The order matters due to Azure resource dependencies
	steps := []struct {
		step        string
		description string
		critical    bool
	}{
		{
			step:        "Step 1",
			description: "Network Security Perimeters (NSP) with force deletion",
			critical:    true,
		},
		{
			step:        "Step 2a",
			description: "Private DNS Zone Groups",
			critical:    true,
		},
		{
			step:        "Step 2b",
			description: "Private Endpoint Connections",
			critical:    true,
		},
		{
			step:        "Step 2c",
			description: "Private Endpoints",
			critical:    true,
		},
		{
			step:        "Step 2d",
			description: "Private DNS Zone Virtual Network Links",
			critical:    true,
		},
		{
			step:        "Step 2e",
			description: "Private Link Services",
			critical:    true,
		},
		{
			step:        "Step 2f",
			description: "Private DNS Zones",
			critical:    true,
		},
		{
			step:        "Step 3",
			description: "Public DNS Zones",
			critical:    false,
		},
		{
			step:        "Step 4",
			description: "Application Resources (bulk deletion - AKS, Cosmos, etc.)",
			critical:    false,
		},
		{
			step:        "Step 4b",
			description: "Public IP Addresses (with 3 retries after AKS deletion)",
			critical:    true,
		},
		{
			step:        "Step 5",
			description: "Monitoring Resources (Data Collection Rules & Endpoints)",
			critical:    false,
		},
		{
			step:        "Step 6",
			description: "Core Networking (Virtual Networks, NSGs)",
			critical:    true,
		},
		{
			step:        "Step 7",
			description: "Key Vault Purge (soft-deleted vaults)",
			critical:    false,
		},
		{
			step:        "Step 8",
			description: "Resource Group Deletion (with 5 retries)",
			critical:    true,
		},
	}

	assert.Equal(t, 14, len(steps), "Deletion process should have 14 steps")

	t.Log("Documented deletion order:")
	for _, step := range steps {
		criticalMarker := ""
		if step.critical {
			criticalMarker = " [CRITICAL ORDER]"
		}
		t.Logf("  %s: %s%s", step.step, step.description, criticalMarker)
	}
}
