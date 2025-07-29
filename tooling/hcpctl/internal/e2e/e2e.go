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

	"gopkg.in/yaml.v3"
)

const (
	// Test configuration
	DefaultTimeout = 1 * time.Minute
	HCPTimeout     = 1 * time.Minute

	// Binary path
	HCPCTLBinary = "../../hcpctl"
)

var (
	// DefaultMCCluster can be overridden via E2E_MC_CLUSTER environment variable
	DefaultMCCluster = os.Getenv("E2E_MC_CLUSTER")
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

// createTempKubeconfig creates a temporary kubeconfig file path
func createTempKubeconfig(t *testing.T, prefix string) string {
	t.Helper()
	tempDir := t.TempDir()
	return filepath.Join(tempDir, fmt.Sprintf("%s-kubeconfig.yaml", prefix))
}
