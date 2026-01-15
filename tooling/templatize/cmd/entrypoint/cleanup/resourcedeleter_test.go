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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExcludedFromBulkDeletion(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		expected     bool
	}{
		{
			name:         "Network Security Perimeter excluded",
			resourceType: "Microsoft.Network/networkSecurityPerimeters",
			expected:     true,
		},
		{
			name:         "Public IP excluded",
			resourceType: "Microsoft.Network/publicIPAddresses",
			expected:     true,
		},
		{
			name:         "Private DNS Zone excluded",
			resourceType: "Microsoft.Network/privateDnsZones",
			expected:     true,
		},
		{
			name:         "Private DNS Zone Virtual Network Link excluded",
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			expected:     true,
		},
		{
			name:         "Public DNS Zone excluded",
			resourceType: "Microsoft.Network/dnszones",
			expected:     true,
		},
		{
			name:         "Virtual Network excluded",
			resourceType: "Microsoft.Network/virtualNetworks",
			expected:     true,
		},
		{
			name:         "Network Security Group excluded",
			resourceType: "Microsoft.Network/networkSecurityGroups",
			expected:     true,
		},
		{
			name:         "Regular resource not excluded",
			resourceType: "Microsoft.Compute/virtualMachines",
			expected:     false,
		},
		{
			name:         "Storage account not excluded",
			resourceType: "Microsoft.Storage/storageAccounts",
			expected:     false,
		},
		{
			name:         "AKS cluster not excluded",
			resourceType: "Microsoft.ContainerService/managedClusters",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			excluded := false
			for _, excludedType := range excludedFromBulkDeletion {
				if strings.EqualFold(excludedType, tt.resourceType) {
					excluded = true
					break
				}
			}
			assert.Equal(t, tt.expected, excluded)
		})
	}
}

func TestExcludedFromBulkDeletionCompleteness(t *testing.T) {
	// Verify all expected types are present in the exclusion list
	expectedTypes := []struct {
		resourceType string
		reason       string
	}{
		{
			resourceType: "Microsoft.Network/networkSecurityPerimeters",
			reason:       "Step 1: Deleted first with force flag",
		},
		{
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			reason:       "Step 2d: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/privateEndpoints/privateDnsZoneGroups",
			reason:       "Step 2a: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/privateEndpointConnections",
			reason:       "Step 2b: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/privateEndpoints",
			reason:       "Step 2c: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/privateLinkServices",
			reason:       "Step 2e: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/privateDnsZones",
			reason:       "Step 2f: Deleted in private networking phase",
		},
		{
			resourceType: "Microsoft.Network/dnszones",
			reason:       "Step 3: Public DNS zones deleted separately",
		},
		{
			resourceType: "Microsoft.Insights/dataCollectionRules",
			reason:       "Step 5: Monitoring resources deleted separately",
		},
		{
			resourceType: "Microsoft.Insights/dataCollectionEndpoints",
			reason:       "Step 5: Monitoring resources deleted separately",
		},
		{
			resourceType: "Microsoft.Network/virtualNetworks",
			reason:       "Step 6: Core networking deleted separately",
		},
		{
			resourceType: "Microsoft.Network/networkSecurityGroups",
			reason:       "Step 6: Core networking deleted separately",
		},
		{
			resourceType: "Microsoft.Network/publicIPAddresses",
			reason:       "Step 4b: Public IPs deleted after AKS with retries",
		},
		{
			resourceType: "Microsoft.ContainerInstance/containerGroups",
			reason:       "Excluded to avoid disruption during cleanup",
		},
	}

	assert.Equal(t, len(expectedTypes), len(excludedFromBulkDeletion),
		"excludedFromBulkDeletion should contain exactly %d types", len(expectedTypes))

	for _, expected := range expectedTypes {
		t.Run(expected.resourceType, func(t *testing.T) {
			found := false
			for _, excludedType := range excludedFromBulkDeletion {
				if strings.EqualFold(excludedType, expected.resourceType) {
					found = true
					break
				}
			}
			assert.True(t, found, "%s not found in exclusion list (reason: %s)",
				expected.resourceType, expected.reason)
		})
	}
}

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

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		actual   int
		expected int
		message  string
	}{
		{
			name:     "maxRetries",
			actual:   maxRetries,
			expected: 3,
			message:  "Standard retry count for most operations",
		},
		{
			name:     "cosmosMaxRetries",
			actual:   cosmosMaxRetries,
			expected: 3,
			message:  "Cosmos DB retry count (matches bash script)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.actual, tt.message)
		})
	}
}

func TestGetAPIVersionForResourceType(t *testing.T) {
	tests := []struct {
		name          string
		resourceType  string
		expectError   bool
		errorContains string
		reason        string
	}{
		{
			name:          "Invalid format - returns error",
			resourceType:  "InvalidResourceType",
			expectError:   true,
			errorContains: "invalid resource type format",
			reason:        "Returns error for invalid format",
		},
		{
			name:          "Empty resource type - returns error",
			resourceType:  "",
			expectError:   true,
			errorContains: "invalid resource type format",
			reason:        "Returns error for empty resource type",
		},
		{
			name:          "Resource type without slash - returns error",
			resourceType:  "NoSlashHere",
			expectError:   true,
			errorContains: "invalid resource type format",
			reason:        "Returns error for resource type without slash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &resourceGroupDeleter{}
			_, err := d.getAPIVersionForResourceType(tt.resourceType)

			if tt.expectError {
				assert.Error(t, err, tt.reason)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains, "Error message should contain expected text")
				}
			} else {
				assert.NoError(t, err, tt.reason)
			}
		})
	}
}
