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

package mc

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
)

// TestMCCommandRequiresArgument tests that the MC command requires an AKS_NAME argument
func TestMCCommandRequiresArgument(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatalf("Failed to create MC command: %v", err)
	}

	// Test that command expects exactly 1 argument
	if cmd.Args == nil {
		t.Fatal("Expected command to have argument validation")
	}

	// Test no arguments - should fail
	err = cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("Expected error when no arguments provided")
	}

	// Test too many arguments - should fail
	err = cmd.Args(cmd, []string{"cluster1", "cluster2"})
	if err == nil {
		t.Error("Expected error when too many arguments provided")
	}

	// Test exactly one argument - should pass
	err = cmd.Args(cmd, []string{"test-cluster"})
	if err != nil {
		t.Errorf("Expected no error with one argument, got: %v", err)
	}
}

// TestMCCommandUsage tests that the command usage shows mandatory argument
func TestMCCommandUsage(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatalf("Failed to create MC command: %v", err)
	}

	// Check that Usage shows mandatory argument format
	expectedUsage := "mc AKS_NAME"
	if cmd.Use != expectedUsage {
		t.Errorf("Expected usage '%s', got '%s'", expectedUsage, cmd.Use)
	}

	// Check that long description mentions list command
	if cmd.Long == "" {
		t.Error("Expected command to have long description")
	}

	// Should mention the list command for discovering clusters
	longDesc := cmd.Long
	if !contains(longDesc, "hcpctl breakglass mc list") {
		t.Error("Expected long description to mention 'hcpctl breakglass mc list' command")
	}

	// Should not mention interactive selection anymore
	if contains(longDesc, "interactive cluster selection") || contains(longDesc, "selection menu") {
		t.Error("Long description should not mention interactive cluster selection")
	}
}

// contains is a helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			indexOf(s, substr) >= 0))
}

// indexOf returns the index of substr in s, or -1 if not found
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestRegionFiltering tests the region filtering logic
func TestRegionFiltering(t *testing.T) {
	// Test data
	clusters := []cluster.AKSCluster{
		{Name: "cluster1", Location: "eastus", ResourceGroup: "rg1", SubscriptionID: "sub1", Subscription: "Sub1"},
		{Name: "cluster2", Location: "westus2", ResourceGroup: "rg2", SubscriptionID: "sub2", Subscription: "Sub2"},
		{Name: "cluster3", Location: "eastus", ResourceGroup: "rg3", SubscriptionID: "sub3", Subscription: "Sub3"},
		{Name: "cluster4", Location: "centralus", ResourceGroup: "rg4", SubscriptionID: "sub4", Subscription: "Sub4"},
	}

	tests := []struct {
		name          string
		regionFilter  string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "No region filter",
			regionFilter:  "",
			expectedCount: 4,
			expectedNames: []string{"cluster1", "cluster2", "cluster3", "cluster4"},
		},
		{
			name:          "Filter by eastus",
			regionFilter:  "eastus",
			expectedCount: 2,
			expectedNames: []string{"cluster1", "cluster3"},
		},
		{
			name:          "Filter by westus2",
			regionFilter:  "westus2",
			expectedCount: 1,
			expectedNames: []string{"cluster2"},
		},
		{
			name:          "Filter by centralus",
			regionFilter:  "centralus",
			expectedCount: 1,
			expectedNames: []string{"cluster4"},
		},
		{
			name:          "Filter by unknown region",
			regionFilter:  "northeurope",
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name:          "Case insensitive filtering - EASTUS",
			regionFilter:  "EASTUS",
			expectedCount: 2,
			expectedNames: []string{"cluster1", "cluster3"},
		},
		{
			name:          "Case insensitive filtering - WestUs2",
			regionFilter:  "WestUs2",
			expectedCount: 1,
			expectedNames: []string{"cluster2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply region filtering logic (same as in runListClusters)
			filteredClusters := clusters
			if tt.regionFilter != "" {
				filtered := make([]cluster.AKSCluster, 0)
				regionFilter := strings.ToLower(tt.regionFilter)

				for _, cluster := range clusters {
					if strings.ToLower(cluster.Location) == regionFilter {
						filtered = append(filtered, cluster)
					}
				}
				filteredClusters = filtered
			}

			// Check count
			if len(filteredClusters) != tt.expectedCount {
				t.Errorf("Expected %d clusters, got %d", tt.expectedCount, len(filteredClusters))
			}

			// Check cluster names
			actualNames := make([]string, len(filteredClusters))
			for i, cluster := range filteredClusters {
				actualNames[i] = cluster.Name
			}

			if len(actualNames) != len(tt.expectedNames) {
				t.Errorf("Expected cluster names %v, got %v", tt.expectedNames, actualNames)
				return
			}

			for _, expectedName := range tt.expectedNames {
				found := false
				for _, actualName := range actualNames {
					if actualName == expectedName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected cluster '%s' not found in filtered results", expectedName)
				}
			}
		})
	}
}

// TestRegionFlagBinding tests that the --region flag is properly bound to list subcommand
func TestRegionFlagBinding(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatalf("Failed to create MC command: %v", err)
	}

	// Get the list subcommand
	listCmd := cmd.Commands()[0] // Should be the list command
	if listCmd.Name() != "list" {
		t.Fatalf("Expected first subcommand to be 'list', got '%s'", listCmd.Name())
	}

	// Check that --region flag exists on list subcommand
	regionFlag := listCmd.Flags().Lookup("region")
	if regionFlag == nil {
		t.Error("Expected --region flag to be defined on list subcommand")
		return
	}

	// Check flag usage text
	if regionFlag.Usage == "" {
		t.Error("Expected --region flag to have usage text")
	}

	// Check that flag usage mentions Azure region
	if !strings.Contains(regionFlag.Usage, "region") {
		t.Error("Expected --region flag usage to mention 'region'")
	}

	// Verify flag is NOT on main command
	mainRegionFlag := cmd.Flags().Lookup("region")
	if mainRegionFlag != nil {
		t.Error("Expected --region flag to NOT be on main command, only on list subcommand")
	}
}
