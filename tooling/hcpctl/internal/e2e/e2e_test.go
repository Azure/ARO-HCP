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

//go:build ToolingE2Etests

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/release"
)

const (
	// Test configuration
	DefaultTimeout = 1 * time.Minute
	HCPTimeout     = 1 * time.Minute

	// Binary path
	HCPCTLBinary = "../../hcpctl"
)

var (
	DefaultMCCluster = os.Getenv("E2E_MC_CLUSTER")
	DefaultSCCluster = os.Getenv("E2E_SC_CLUSTER")
)

// TestCluster represents cluster information for testing
type TestCluster struct {
	Name string
	Type string // "mc", "sc", "hcp"
}

// AuthOutput represents the structure of kubectl auth whoami -o yaml output
type AuthOutput struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Status     struct {
		UserInfo struct {
			Username string              `yaml:"username"`
			Groups   []string            `yaml:"groups"`
			Extra    map[string][]string `yaml:"extra,omitempty"`
		} `yaml:"userInfo"`
	} `yaml:"status"`
}

// ClusterListWrapper represents the wrapper structure for cluster list outputs
type ClusterListWrapper struct {
	Items []interface{} `yaml:"items"`
}

// SCCluster represents SC cluster information from sc list output
type SCCluster struct {
	Name           string `yaml:"name" json:"name"`
	Location       string `yaml:"location" json:"location"`
	ResourceGroup  string `yaml:"resourcegroup" json:"resourcegroup"`
	SubscriptionID string `yaml:"subscriptionid" json:"subscriptionid"`
	State          string `yaml:"state" json:"state"`
}

// MCCluster represents MC cluster information from mc list output
type MCCluster struct {
	Name           string `yaml:"name" json:"name"`
	Location       string `yaml:"location" json:"location"`
	ResourceGroup  string `yaml:"resourcegroup" json:"resourcegroup"`
	SubscriptionID string `yaml:"subscription" json:"subscription"`
	State          string `yaml:"state" json:"state"`
}

// HCPCluster represents HCP cluster information from hcp list output
type HCPCluster struct {
	ID             string `yaml:"id"`
	Name           string `yaml:"name"`
	SubscriptionID string `yaml:"subscriptionId"`
}

// execCommand executes a command with timeout and returns output
func execCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	return cmd.Output()
}

// execCommandWithKubeconfig executes a command with a specific kubeconfig
func execCommandWithKubeconfig(ctx context.Context, kubeconfig, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	return cmd.Output()
}

// execCommandWithKubeconfigAndStderr executes a command with a specific kubeconfig and captures both stdout and stderr
func execCommandWithKubeconfigAndStderr(ctx context.Context, kubeconfig, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	stdout, err := cmd.Output()
	var stderr []byte
	if exitError, ok := err.(*exec.ExitError); ok {
		stderr = exitError.Stderr
	}
	return stdout, stderr, err
}

// parseAuthOutput parses kubectl auth whoami YAML output
func parseAuthOutput(data []byte) (*AuthOutput, error) {
	var auth AuthOutput
	if err := yaml.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("failed to parse auth output: %w", err)
	}
	return &auth, nil
}

// parseSCListYAML parses sc list YAML output
func parseSCListYAML(data []byte) ([]SCCluster, error) {
	var wrapper struct {
		Items []SCCluster `yaml:"items"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse SC list YAML output: %w", err)
	}
	return wrapper.Items, nil
}

// parseSCListJSON parses sc list JSON output
func parseSCListJSON(data []byte) ([]SCCluster, error) {
	var wrapper struct {
		Items []SCCluster `json:"items"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse SC list JSON output: %w", err)
	}
	return wrapper.Items, nil
}

// parseSCList parses sc list output (legacy, defaults to YAML)
func parseSCList(data []byte) ([]SCCluster, error) {
	return parseSCListYAML(data)
}

// parseMCListYAML parses mc list YAML output
func parseMCListYAML(data []byte) ([]MCCluster, error) {
	var wrapper struct {
		Items []MCCluster `yaml:"items"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse MC list YAML output: %w", err)
	}
	return wrapper.Items, nil
}

// parseMCListJSON parses mc list JSON output
func parseMCListJSON(data []byte) ([]MCCluster, error) {
	var wrapper struct {
		Items []MCCluster `json:"items"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse MC list JSON output: %w", err)
	}
	return wrapper.Items, nil
}

// parseHCPList parses hcp list YAML output
func parseHCPList(data []byte) ([]HCPCluster, error) {
	var wrapper struct {
		Items []HCPCluster `yaml:"items"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse HCP list output: %w", err)
	}
	return wrapper.Items, nil
}

// hasGroup checks if the user has any of the specified groups
func hasGroup(auth *AuthOutput, groups ...string) bool {
	userGroups := auth.Status.UserInfo.Groups
	for _, userGroup := range userGroups {
		for _, requiredGroup := range groups {
			if strings.Contains(userGroup, requiredGroup) {
				return true
			}
		}
	}
	return false
}

// parseReleaseStatusYAML parses hcpctl release status YAML output
func parseReleaseStatusYAML(data []byte) (*release.ClusterComponentRelease, error) {
	var clusterRelease release.ClusterComponentRelease
	if err := yaml.Unmarshal(data, &clusterRelease); err != nil {
		return nil, fmt.Errorf("failed to parse release status YAML output: %w", err)
	}
	return &clusterRelease, nil
}

// createTempKubeconfig creates a temporary kubeconfig file path
func createTempKubeconfig(t *testing.T, prefix string) string {
	t.Helper()
	tempDir := t.TempDir()
	return filepath.Join(tempDir, fmt.Sprintf("%s-kubeconfig.yaml", prefix))
}

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

	hcpCluster := hcpClusters[len(hcpClusters)-1]
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

func TestReleaseStatus(t *testing.T) {
	tests := []struct {
		name         string
		outputFormat string
		releaseName  string
		namespace    string
	}{
		{
			name:         "release status in SC context",
			outputFormat: "yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			// Create a temporary kubeconfig for this test
			kubeconfigPath := createTempKubeconfig(t, "sc-release-status")

			// Get SC access via breakglass
			_, err := execCommand(ctx, HCPCTLBinary, "sc", "breakglass", DefaultSCCluster, "--no-shell", "--output", kubeconfigPath)
			if err != nil {
				t.Fatalf("sc breakglass failed: %v", err)
			}

			// Verify kubeconfig was created
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				t.Fatal("kubeconfig file was not created")
			}

			// Build release status command args
			args := []string{"release", "status", "--kubeconfig", kubeconfigPath, "-o", tt.outputFormat}
			if tt.releaseName != "" {
				args = append(args, "--release", tt.releaseName)
			}
			if tt.namespace != "" {
				args = append(args, "--namespace", tt.namespace)
			}

			// Execute release status command
			output, err := execCommand(ctx, HCPCTLBinary, args...)

			if err != nil {
				t.Fatalf("release status failed: %v", err)
			}

			if len(output) == 0 {
				t.Fatal("release status returned empty output")
			}

			// Parse
			clusterRelease, err := parseReleaseStatusYAML(output)
			if err != nil {
				t.Fatalf("failed to parse release status YAML: %v", err)
			}

			// Validate the structure
			if clusterRelease.APIVersion != "service-status.hcm.openshift.io/v1" {
				t.Errorf("unexpected APIVersion: got %s, want service-status.hcm.openshift.io/v1", clusterRelease.APIVersion)
			}
			if clusterRelease.Kind != "ClusterComponentRelease" {
				t.Errorf("unexpected Kind: got %s, want ClusterComponentRelease", clusterRelease.Kind)
			}
			if clusterRelease.Metadata.Name == "" {
				t.Error("metadata.name is empty")
			}
			if clusterRelease.Metadata.CreationTimestamp.IsZero() {
				t.Error("metadata.creationTimestamp is zero")
			}

			// Validate each component has expected structure
			for i, component := range clusterRelease.Components {
				if component.APIVersion != "service-status.hcm.openshift.io/v1" {
					t.Errorf("component %d has unexpected APIVersion: got %s, want service-status.hcm.openshift.io/v1", i, component.APIVersion)
				}
				if component.Kind != "ComponentRelease" {
					t.Errorf("component %d has unexpected Kind: got %s, want ComponentRelease", i, component.Kind)
				}
				if component.Metadata.Name == "" {
					t.Errorf("component %d has empty metadata.name", i)
				}

				// Validate workloads have required fields
				for j, workload := range component.Workloads {
					if workload.Name == "" {
						t.Errorf("component %d, workload %d has empty name", i, j)
					}
					if workload.Kind == "" {
						t.Errorf("component %d, workload %d has empty kind", i, j)
					}
					if workload.DesiredImage == "" {
						t.Errorf("component %d, workload %d has empty desiredImage", i, j)
					}
					// CurrentImage can be empty or "NOT_FOUND" if workload is not actually deployed
				}

				t.Logf("  Component [%d]: %s (%d workloads)", i+1, component.Metadata.Name, len(component.Workloads))
			}
		})
	}
}
