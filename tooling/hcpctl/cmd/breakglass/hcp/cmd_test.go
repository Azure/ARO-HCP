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

package hcp

import (
	"testing"
)

// TestHCPCommandRequiresArgument tests that the HCP command requires a CLUSTER_ID_OR_RESOURCE_ID argument
func TestHCPCommandRequiresArgument(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatalf("Failed to create HCP command: %v", err)
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
	err = cmd.Args(cmd, []string{"2jesjug41iavg27inj078ssjidn20clk"})
	if err != nil {
		t.Errorf("Expected no error with one argument, got: %v", err)
	}
}

// TestHCPCommandUsage tests that the command usage shows mandatory argument
func TestHCPCommandUsage(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatalf("Failed to create HCP command: %v", err)
	}

	// Check that Usage shows mandatory argument format
	expectedUsage := "hcp CLUSTER_ID_OR_RESOURCE_ID"
	if cmd.Use != expectedUsage {
		t.Errorf("Expected usage '%s', got '%s'", expectedUsage, cmd.Use)
	}

	// Check that long description mentions list command
	if cmd.Long == "" {
		t.Error("Expected command to have long description")
	}

	// Should mention the list command for discovering clusters
	longDesc := cmd.Long
	if !contains(longDesc, "hcpctl breakglass hcp list") {
		t.Error("Expected long description to mention 'hcpctl breakglass hcp list' command")
	}

	// Should not mention interactive selection anymore
	if contains(longDesc, "interactive cluster selection menu") {
		t.Error("Long description should not mention interactive cluster selection menu")
	}
}

// contains is a helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
