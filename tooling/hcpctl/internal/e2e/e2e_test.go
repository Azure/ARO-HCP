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

//go:build E2Etests

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Check if binary exists, if not, try to build it
	if _, err := os.Stat(HCPCTLBinary); os.IsNotExist(err) {
		fmt.Printf("hcpctl binary not found at %s. Building...\n", HCPCTLBinary)

		// Run make build from the project root (two levels up from internal/e2e)
		buildCmd := exec.Command("make", "build")
		buildCmd.Dir = "../../" // Go to project root
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr

		if err := buildCmd.Run(); err != nil {
			fmt.Printf("Failed to build hcpctl binary: %v\n", err)
			os.Exit(1)
		}

		// Verify the binary was created
		if _, err := os.Stat(HCPCTLBinary); os.IsNotExist(err) {
			fmt.Printf("hcpctl binary still not found at %s after build\n", HCPCTLBinary)
			os.Exit(1)
		}

		fmt.Println("Build successful!")
	}

	// Run tests
	os.Exit(m.Run())
}

func TestMCList(t *testing.T) {
	tests := []struct {
		name             string
		outputFormat     string
		region           string
		clustersExpected bool
		expectError      bool
	}{
		{
			name:             "list MC clusters in YAML format",
			outputFormat:     "yaml",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list MC clusters in JSON format",
			outputFormat:     "json",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list MC clusters in YAML format in a specific region",
			outputFormat:     "yaml",
			region:           "westus3",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list MC clusters in YAML format in a non-existing region",
			outputFormat:     "yaml",
			region:           "easternplaguelands",
			expectError:      false,
			clustersExpected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			args := []string{"mc", "list"}
			if tt.region != "" {
				args = append(args, "--region", tt.region)
			}
			if tt.outputFormat != "" {
				args = append(args, "-o", tt.outputFormat)
			}
			output, err := execCommand(ctx, HCPCTLBinary, args...)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("mc list failed: %v", err)
			}

			if len(output) == 0 {
				t.Fatal("mc list returned empty output")
			}

			// Parse and validate structure based on format
			var mcClusters []MCCluster
			switch tt.outputFormat {
			case "yaml":
				mcClusters, err = parseMCListYAML(output)
				if err != nil {
					t.Fatalf("failed to parse MC list YAML: %v", err)
				}
			case "json":
				mcClusters, err = parseMCListJSON(output)
				if err != nil {
					t.Fatalf("failed to parse MC list JSON: %v", err)
				}
			default:
				t.Fatalf("unsupported output format: %s", tt.outputFormat)
			}

			// Check if we got clusters based on expectation
			if tt.clustersExpected && len(mcClusters) == 0 {
				t.Fatal("expected MC clusters but found none")
			} else if !tt.clustersExpected && len(mcClusters) > 0 {
				t.Fatalf("expected no MC clusters but found %d", len(mcClusters))
			}

			// Only validate cluster structure if clusters were expected and found
			if tt.clustersExpected && len(mcClusters) > 0 {
				// Validate structure of all clusters
				for i, cluster := range mcClusters {
					if cluster.Name == "" {
						t.Errorf("MC cluster %d has empty name", i)
					}
					if cluster.Location == "" {
						t.Errorf("MC cluster %d has empty location", i)
					}
					if cluster.ResourceGroup == "" {
						t.Errorf("MC cluster %d has empty resourcegroup", i)
					}
					if cluster.SubscriptionID == "" {
						t.Errorf("MC cluster %d has empty subscription", i)
					}
				}

				t.Logf("MC list (%s): found %d clusters",
					tt.outputFormat, len(mcClusters))
			} else if !tt.clustersExpected {
				t.Logf("MC list (%s): no clusters found as expected for region %s",
					tt.outputFormat, tt.region)
			}
		})
	}
}

func TestSCList(t *testing.T) {
	tests := []struct {
		name             string
		outputFormat     string
		region           string
		clustersExpected bool
		expectError      bool
	}{
		{
			name:             "list SC clusters in YAML format",
			outputFormat:     "yaml",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list SC clusters in JSON format",
			outputFormat:     "json",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list SC clusters in YAML format in a specific region",
			outputFormat:     "yaml",
			region:           "westus3",
			expectError:      false,
			clustersExpected: true,
		},
		{
			name:             "list SC clusters in YAML format in a non-existing region",
			outputFormat:     "yaml",
			region:           "easternplaguelands",
			expectError:      false,
			clustersExpected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			args := []string{"sc", "list"}
			if tt.region != "" {
				args = append(args, "--region", tt.region)
			}
			if tt.outputFormat != "" {
				args = append(args, "-o", tt.outputFormat)
			}
			output, err := execCommand(ctx, HCPCTLBinary, args...)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("sc list failed: %v", err)
			}

			if len(output) == 0 {
				t.Fatal("sc list returned empty output")
			}

			// Parse and validate structure based on format
			var scClusters []SCCluster
			switch tt.outputFormat {
			case "yaml":
				scClusters, err = parseSCListYAML(output)
				if err != nil {
					t.Fatalf("failed to parse SC list YAML: %v", err)
				}
			case "json":
				scClusters, err = parseSCListJSON(output)
				if err != nil {
					t.Fatalf("failed to parse SC list JSON: %v", err)
				}
			default:
				t.Fatalf("unsupported output format: %s", tt.outputFormat)
			}

			// Check if we got clusters based on expectation
			if tt.clustersExpected && len(scClusters) == 0 {
				t.Fatal("expected SC clusters but found none")
			} else if !tt.clustersExpected && len(scClusters) > 0 {
				t.Fatalf("expected no SC clusters but found %d", len(scClusters))
			}

			// Only validate cluster structure if clusters were expected and found
			if tt.clustersExpected && len(scClusters) > 0 {
				// Validate structure of all clusters
				for i, cluster := range scClusters {
					if cluster.Name == "" {
						t.Errorf("SC cluster %d has empty name", i)
					}
					if cluster.Location == "" {
						t.Errorf("SC cluster %d has empty location", i)
					}
					if cluster.ResourceGroup == "" {
						t.Errorf("SC cluster %d has empty resourcegroup", i)
					}
					if cluster.SubscriptionID == "" {
						t.Errorf("SC cluster %d has empty subscription", i)
					}
				}

				t.Logf("SC list (%s): found %d clusters",
					tt.outputFormat, len(scClusters))
			} else if !tt.clustersExpected {
				t.Logf("SC list (%s): no clusters found as expected for region %s",
					tt.outputFormat, tt.region)
			}
		})
	}
}

func TestMCBreakglass(t *testing.T) {
	tests := []struct {
		name        string
		cluster     string
		useExec     bool
		expectError bool
	}{
		{
			name:        "MC breakglass with kubeconfig generation",
			cluster:     DefaultMCCluster,
			useExec:     false,
			expectError: false,
		},
		{
			name:        "MC breakglass with --exec kubectl auth whoami",
			cluster:     DefaultMCCluster,
			useExec:     true,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			var authOutput []byte
			var err error

			if tt.useExec {
				// Test with --exec flag
				authOutput, err = execCommand(ctx, HCPCTLBinary, "mc", "breakglass", tt.cluster, "--exec", "kubectl auth whoami -o yaml")

				if tt.expectError {
					if err == nil {
						t.Errorf("expected error, but got none")
					}
					return
				}

				if err != nil {
					t.Fatalf("mc breakglass --exec failed: %v", err)
				}
			} else {
				// Test kubeconfig generation with --no-shell
				kubeconfigPath := createTempKubeconfig(t, "mc")

				_, err := execCommand(ctx, HCPCTLBinary, "mc", "breakglass", tt.cluster, "--no-shell", "--output", kubeconfigPath)

				if tt.expectError {
					if err == nil {
						t.Errorf("expected error, but got none")
					}
					return
				}

				if err != nil {
					t.Fatalf("mc breakglass failed: %v", err)
				}

				// Verify kubeconfig was created
				if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
					t.Fatal("kubeconfig file was not created")
				}

				// Test authentication with generated kubeconfig
				authOutput, err = execCommandWithKubeconfig(ctx, kubeconfigPath, "kubectl", "auth", "whoami", "-o", "yaml")
				if err != nil {
					t.Fatalf("kubectl auth whoami failed: %v", err)
				}
			}

			if len(authOutput) == 0 {
				t.Fatal("kubectl auth whoami returned empty output")
			}

			// Parse and validate authentication output
			auth, err := parseAuthOutput(authOutput)
			if err != nil {
				t.Fatalf("failed to parse auth output: %v", err)
			}

			// Validate user has system:authenticated group
			if !hasGroup(auth, "system:authenticated") {
				t.Errorf("user does not have system:authenticated group, got groups: %v", auth.Status.UserInfo.Groups)
			}

			accessMethod := "kubeconfig"
			if tt.useExec {
				accessMethod = "--exec"
			}
			t.Logf("MC auth successful (%s): user=%s, groups=%v", accessMethod, auth.Status.UserInfo.Username, auth.Status.UserInfo.Groups)
		})
	}
}

func TestSCBreakglass(t *testing.T) {
	// First get an available SC cluster
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	scListOutput, err := execCommand(ctx, HCPCTLBinary, "sc", "list", "-o", "yaml")
	if err != nil {
		t.Skipf("Failed to list SC clusters: %v", err)
	}

	scClusters, err := parseSCList(scListOutput)
	if err != nil {
		t.Skipf("Failed to parse SC list: %v", err)
	}

	if len(scClusters) == 0 {
		t.Skip("No SC clusters available for testing")
	}

	scCluster := scClusters[0].Name

	tests := []struct {
		name        string
		cluster     string
		useExec     bool
		expectError bool
	}{
		{
			name:        "SC breakglass with kubeconfig generation",
			cluster:     scCluster,
			useExec:     false,
			expectError: false,
		},
		{
			name:        "SC breakglass with --exec kubectl auth whoami",
			cluster:     scCluster,
			useExec:     true,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			var authOutput []byte
			var err error

			if tt.useExec {
				// Test with --exec flag
				authOutput, err = execCommand(ctx, HCPCTLBinary, "sc", "breakglass", tt.cluster, "--exec", "kubectl auth whoami -o yaml")

				if tt.expectError {
					if err == nil {
						t.Errorf("expected error, but got none")
					}
					return
				}

				if err != nil {
					t.Fatalf("sc breakglass --exec failed: %v", err)
				}
			} else {
				// Test kubeconfig generation with --no-shell
				kubeconfigPath := createTempKubeconfig(t, "sc")

				_, err := execCommand(ctx, HCPCTLBinary, "sc", "breakglass", tt.cluster, "--no-shell", "--output", kubeconfigPath)

				if tt.expectError {
					if err == nil {
						t.Errorf("expected error, but got none")
					}
					return
				}

				if err != nil {
					t.Fatalf("sc breakglass failed: %v", err)
				}

				// Verify kubeconfig was created
				if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
					t.Fatal("kubeconfig file was not created")
				}

				// Test authentication with generated kubeconfig
				authOutput, err = execCommandWithKubeconfig(ctx, kubeconfigPath, "kubectl", "auth", "whoami", "-o", "yaml")
				if err != nil {
					t.Fatalf("kubectl auth whoami failed: %v", err)
				}
			}

			if len(authOutput) == 0 {
				t.Fatal("kubectl auth whoami returned empty output")
			}

			// Parse and validate authentication output
			auth, err := parseAuthOutput(authOutput)
			if err != nil {
				t.Fatalf("failed to parse auth output: %v", err)
			}

			// Validate user has system:authenticated group
			if !hasGroup(auth, "system:authenticated") {
				t.Errorf("user does not have system:authenticated group, got groups: %v", auth.Status.UserInfo.Groups)
			}

			accessMethod := "kubeconfig"
			if tt.useExec {
				accessMethod = "--exec"
			}
			t.Logf("SC auth successful (%s): user=%s, groups=%v", accessMethod, auth.Status.UserInfo.Username, auth.Status.UserInfo.Groups)
		})
	}
}

func TestHCPBreakglassRole(t *testing.T) {
	// Get available HCP clusters (this is read-only and safe to share)
	ctx, cancel := context.WithTimeout(context.Background(), HCPTimeout)
	defer cancel()

	// Get MC kubeconfig for cluster discovery
	mcKubeconfigPath := createTempKubeconfig(t, "mc-for-hcp-discovery")
	_, err := execCommand(ctx, HCPCTLBinary, "mc", "breakglass", DefaultMCCluster, "--no-shell", "--output", mcKubeconfigPath)
	if err != nil {
		t.Fatalf("Failed to establish MC context: %v", err)
	}

	hcpListOutput, err := execCommandWithKubeconfig(ctx, mcKubeconfigPath, HCPCTLBinary, "hcp", "list", "-o", "yaml")
	if err != nil {
		t.Skipf("Failed to list HCP clusters from MC context: %v", err)
	}

	hcpClusters, err := parseHCPList(hcpListOutput)
	if err != nil {
		t.Skipf("Failed to parse HCP list: %v", err)
	}

	if len(hcpClusters) == 0 {
		t.Skip("No HCP clusters available for testing")
	}

	hcpCluster := hcpClusters[0]
	t.Logf("Using HCP cluster: %s (name: %s)", hcpCluster.ID, hcpCluster.Name)

	tests := []struct {
		name           string
		cluster        string
		privileged     bool
		expectedGroups []string
		expectError    bool
	}{
		{
			name:           "HCP breakglass regular access role validation",
			cluster:        hcpCluster.ID,
			privileged:     false,
			expectedGroups: []string{"aro-sre"},
			expectError:    false,
		},
		{
			name:           "HCP breakglass privileged access role validation",
			cluster:        hcpCluster.ID,
			privileged:     true,
			expectedGroups: []string{"aro-sre-cluster-admin"},
			expectError:    false,
		},
		{
			name:        "breakglass into non-existing cluster",
			cluster:     "2k8eebcjl70aob1iium1c4ile2vlkh5u",
			privileged:  true,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), HCPTimeout)
			defer cancel()

			// Each parallel test gets its own MC kubeconfig to avoid conflicts
			mcKubeconfigPath := createTempKubeconfig(t, "mc-for-hcp")
			_, err := execCommand(ctx, HCPCTLBinary, "mc", "breakglass", DefaultMCCluster, "--no-shell", "--output", mcKubeconfigPath)
			if err != nil {
				t.Fatalf("Failed to establish MC context: %v", err)
			}

			args := []string{"hcp", "breakglass", tt.cluster, "--kubeconfig", mcKubeconfigPath, "--exec", "kubectl auth whoami -o yaml"}
			if tt.privileged {
				args = append(args, "--privileged")
			}

			output, stderr, err := execCommandWithKubeconfigAndStderr(ctx, mcKubeconfigPath, HCPCTLBinary, args...)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, but got none")
				}
				return
			}

			if err != nil {
				t.Logf("Command args: %v", args)
				t.Logf("Stderr: %s", string(stderr))
				t.Logf("Stdout: %s", string(output))
				t.Fatalf("hcp breakglass --exec failed: %v", err)
			}

			if len(output) == 0 {
				t.Fatal("hcp breakglass --exec returned empty output")
			}

			// Parse and validate authentication output
			auth, err := parseAuthOutput(output)
			if err != nil {
				t.Fatalf("failed to parse auth output: %v", err)
			}

			// Check for expected groups
			if !hasGroup(auth, tt.expectedGroups...) {
				t.Errorf("user does not have required groups %v, got groups: %v", tt.expectedGroups, auth.Status.UserInfo.Groups)
			}
		})
	}
}

func TestHCPList(t *testing.T) {
	tests := []struct {
		name           string
		outputFormat   string
		expectedFields []string
		expectError    bool
	}{
		{
			name:           "list HCP clusters in YAML format from MC context",
			outputFormat:   "yaml",
			expectedFields: []string{"id", "name", "subscriptionId"},
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			// Each parallel test gets its own MC kubeconfig to avoid conflicts
			mcKubeconfigPath := createTempKubeconfig(t, "mc-for-hcp-list")
			_, err := execCommand(ctx, HCPCTLBinary, "mc", "breakglass", DefaultMCCluster, "--no-shell", "--output", mcKubeconfigPath)
			if err != nil {
				t.Fatalf("Failed to establish MC context: %v", err)
			}

			output, err := execCommandWithKubeconfig(ctx, mcKubeconfigPath, HCPCTLBinary, "hcp", "list", "-o", tt.outputFormat)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, but got none")
				}
				return
			}

			if err != nil {
				t.Skipf("hcp list failed (may be normal if no HCP clusters): %v", err)
			}

			if len(output) == 0 {
				t.Skip("hcp list returned empty output (no HCP clusters available)")
			}

			outputStr := string(output)
			for _, field := range tt.expectedFields {
				if !strings.Contains(outputStr, field) {
					t.Errorf("expected field %q not found in output", field)
				}
			}

			// Parse and validate structure
			hcpClusters, err := parseHCPList(output)
			if err != nil {
				t.Fatalf("failed to parse HCP list output: %v", err)
			}

			t.Logf("HCP list successful: found %d clusters", len(hcpClusters))
			for i, cluster := range hcpClusters {
				t.Logf("  [%d] ID: %s, Name: %s, Subscription: %s", i+1, cluster.ID, cluster.Name, cluster.SubscriptionID)
			}
		})
	}
}
