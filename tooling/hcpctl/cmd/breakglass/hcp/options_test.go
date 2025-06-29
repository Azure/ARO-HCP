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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
)

// TestDefaultHCPOptions tests the default HCP options creation.
func TestDefaultHCPOptions(t *testing.T) {
	opts := DefaultHCPOptions()

	if opts.BaseOptions == nil {
		t.Fatal("Expected BaseOptions to be initialized")
	}

	if opts.SessionTimeout != 24*time.Hour {
		t.Errorf("Expected session timeout 24h, got %v", opts.SessionTimeout)
	}

	if opts.NoPortForward {
		t.Error("Expected NoPortForward to be false by default")
	}

	if opts.NoShell {
		t.Error("Expected NoShell to be false by default")
	}
}

// TestBindHCPOptions tests HCP command flag binding.
func TestBindHCPOptions(t *testing.T) {
	opts := DefaultHCPOptions()
	cmd := &cobra.Command{
		Use: "test",
	}

	err := BindHCPOptions(opts, cmd)
	if err != nil {
		t.Fatalf("BindHCPOptions failed: %v", err)
	}

	// Check that all flags were registered (base + HCP-specific)
	expectedFlags := []string{"kubeconfig", "session-timeout", "output", "no-port-forward", "no-shell"}
	for _, flagName := range expectedFlags {
		flag := cmd.Flags().Lookup(flagName)
		if flag == nil {
			t.Errorf("Flag %s was not registered", flagName)
		}
	}

	// Test short flag for output
	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag.Shorthand != "o" {
		t.Errorf("Expected output flag shorthand 'o', got %s", outputFlag.Shorthand)
	}
}

// TestRawHCPOptionsValidation tests the validation of raw HCP options.
func TestRawHCPOptionsValidation(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tempDir := t.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte("fake kubeconfig"), 0644); err != nil {
		t.Fatalf("Failed to create test kubeconfig: %v", err)
	}

	testCases := []struct {
		name        string
		setupFunc   func() *RawHCPOptions
		expectError bool
		errorString string
	}{
		{
			name: "valid cluster ID and timeout but kubeconfig parsing fails",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = "2jesjug41iavg27inj078ssjidn20clk"
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 1 * time.Hour
				return opts
			},
			expectError: true,
			errorString: "failed to get current user",
		},
		{
			name: "empty cluster ID is not allowed",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = ""
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 1 * time.Hour
				return opts
			},
			expectError: true,
			errorString: "cluster identifier is required",
		},
		{
			name: "invalid cluster ID format",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = "-invalid-cluster-id-"
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 1 * time.Hour
				return opts
			},
			expectError: true,
			errorString: "is neither a valid cluster ID nor a valid Azure resource ID",
		},
		{
			name: "cluster ID too long",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = strings.Repeat("a", 64) // maxClusterIDLength + 1
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 1 * time.Hour
				return opts
			},
			expectError: true,
			errorString: "is neither a valid cluster ID nor a valid Azure resource ID",
		},
		{
			name: "session timeout too short",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = "2jesjug41iavg27inj078ssjidn20clk"
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 30 * time.Second
				return opts
			},
			expectError: true,
			errorString: "session timeout must be at least 1 minute",
		},
		{
			name: "session timeout too long",
			setupFunc: func() *RawHCPOptions {
				opts := DefaultHCPOptions()
				opts.ClusterIdentifier = "2jesjug41iavg27inj078ssjidn20clk"
				opts.BaseOptions.KubeconfigPath = kubeconfigPath
				opts.SessionTimeout = 31 * 24 * time.Hour
				return opts
			},
			expectError: true,
			errorString: "session timeout cannot exceed 30 days",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.setupFunc()
			validated, err := opts.Validate(context.Background())

			if tc.expectError {
				if err == nil {
					t.Error("Expected validation error, got nil")
				} else if !strings.Contains(err.Error(), tc.errorString) {
					t.Errorf("Expected error containing '%s', got: %v", tc.errorString, err)
				}
				if validated != nil {
					t.Error("Expected nil ValidatedHCPOptions on error")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
				if validated == nil {
					t.Error("Expected ValidatedHCPOptions, got nil")
				}
			}
		})
	}
}

// TestParseClusterIdentifier tests parsing of cluster identifiers (both IDs and resource IDs).
func TestParseClusterIdentifier(t *testing.T) {
	testCases := []struct {
		input              string
		expectedClusterID  string
		expectedResourceID string
		expectError        bool
		description        string
	}{
		// Cluster ID cases
		{"2jesjug41iavg27inj078ssjidn20clk", "2jesjug41iavg27inj078ssjidn20clk", "", false, "valid 32-char cluster ID"},
		{"abcdef1234567890abcdef1234567890", "abcdef1234567890abcdef1234567890", "", false, "alphanumeric cluster ID"},
		{"", "", "", true, "empty input (not allowed)"},
		{"-invalid", "", "", true, "invalid cluster ID format"},

		// Azure resource ID cases
		{"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			"", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster", false, "valid Azure resource ID"},
		{"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster-123",
			"", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster-123", false, "Azure resource ID with complex cluster name"},
		{"/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/mmazur-rg-03/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/mmazur",
			"", "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/mmazur-rg-03/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/mmazur", false, "Azure resource ID with lowercase 'shift' in OpenShift"},
		{"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/-invalid-cluster",
			"", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/-invalid-cluster", false, "Azure resource ID with any cluster name (validation not performed)"},
		{"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/otherResource/my-cluster",
			"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/otherResource/my-cluster", "", true, "invalid Azure resource type"},
		{"/invalid/resource/id", "/invalid/resource/id", "", true, "malformed resource ID treated as cluster ID"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			parsed, err := cluster.ParseClusterIdentifier(tc.input)
			if err == nil {
				clusterID := parsed.ClusterID
				var resourceID string
				if parsed.ResourceID != nil {
					resourceID = parsed.ResourceID.String()
				}
				// Update test logic to use the parsed result
				if !tc.expectError {
					if clusterID != tc.expectedClusterID {
						t.Errorf("ParseClusterIdentifier('%s') clusterID = '%s', expected '%s'", tc.input, clusterID, tc.expectedClusterID)
					}
					if resourceID != tc.expectedResourceID {
						t.Errorf("ParseClusterIdentifier('%s') resourceID = '%s', expected '%s'", tc.input, resourceID, tc.expectedResourceID)
					}
				}
			} else if !tc.expectError {
				t.Errorf("Unexpected error for input '%s': %v", tc.input, err)
				return
			}

			if tc.expectError && err == nil {
				t.Errorf("Expected error for input '%s', got nil", tc.input)
			}
		})
	}
}
