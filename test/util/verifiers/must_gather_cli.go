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

package verifiers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
)

const (
	kustoCluster = "hcp-dev-us-2"
	kustoRegion  = "eastus2"

	serviceDir            = "service"
	hostedControlPlaneDir = "hosted-control-plane"
	clusterDir            = "cluster"
)

type mustGatherCLITestCase struct {
	name       string
	subcommand string
	extraArgs  []string
	expectDirs []string // directories expected to contain .log files
}

type mustGatherCLIVerifier struct {
	subscriptionID string
	resourceGroup  string
}

func (v mustGatherCLIVerifier) Name() string {
	return "VerifyMustGatherCLI"
}

func (v mustGatherCLIVerifier) Verify(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	binary := os.Getenv("HCPCTL_BINARY")
	if binary == "" {
		path, err := exec.LookPath("hcpctl")
		if err != nil {
			return fmt.Errorf("hcpctl binary not found: set HCPCTL_BINARY env var or ensure hcpctl is in PATH")
		}
		binary = path
	}

	svcCluster, mgmtCluster := infraClusterNames()

	testCases := []mustGatherCLITestCase{
		// query command variations
		{
			name:       "query-basic",
			subcommand: "query",
			extraArgs:  []string{"--subscription-id", v.subscriptionID, "--resource-group", v.resourceGroup},
			expectDirs: []string{serviceDir, hostedControlPlaneDir},
		},
		{
			name:       "query-skip-hcp-logs",
			subcommand: "query",
			extraArgs:  []string{"--subscription-id", v.subscriptionID, "--resource-group", v.resourceGroup, "--skip-hcp-logs"},
			expectDirs: []string{serviceDir},
		},
		{
			name:       "query-skip-kubernetes-events",
			subcommand: "query",
			extraArgs:  []string{"--subscription-id", v.subscriptionID, "--resource-group", v.resourceGroup, "--skip-kubernetes-events-logs"},
			expectDirs: []string{serviceDir, hostedControlPlaneDir},
		},
		{
			name:       "query-collect-systemd-logs",
			subcommand: "query",
			extraArgs:  []string{"--subscription-id", v.subscriptionID, "--resource-group", v.resourceGroup, "--collect-systemd-logs"},
			expectDirs: []string{serviceDir, hostedControlPlaneDir, clusterDir},
		},
		{
			name:       "query-skip-hcp-and-events",
			subcommand: "query",
			extraArgs:  []string{"--subscription-id", v.subscriptionID, "--resource-group", v.resourceGroup, "--skip-hcp-logs", "--skip-kubernetes-events-logs"},
			expectDirs: []string{serviceDir},
		},
	}

	// query-infra variations only if BUILD_ID is set
	if svcCluster != "" && mgmtCluster != "" {
		testCases = append(testCases,
			mustGatherCLITestCase{
				name:       "query-infra-svc",
				subcommand: "query-infra",
				extraArgs:  []string{"--infra-cluster", svcCluster},
				expectDirs: []string{serviceDir, clusterDir},
			},
			mustGatherCLITestCase{
				name:       "query-infra-mgmt",
				subcommand: "query-infra",
				extraArgs:  []string{"--infra-cluster", mgmtCluster},
				expectDirs: []string{serviceDir, clusterDir},
			},
			mustGatherCLITestCase{
				name:       "query-infra-both",
				subcommand: "query-infra",
				extraArgs:  []string{"--infra-cluster", svcCluster, "--infra-cluster", mgmtCluster},
				expectDirs: []string{serviceDir, clusterDir},
			},
		)
	} else {
		logger.Info("Skipping query-infra tests: BUILD_ID not set, cannot derive infra cluster names")
	}

	var errors []string
	for _, tc := range testCases {
		logger.Info("Running must-gather CLI test case", "name", tc.name)
		if err := runTestCase(ctx, binary, tc); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", tc.name, err))
			logger.Error(err, "Test case failed", "name", tc.name)
		} else {
			logger.Info("Test case passed", "name", tc.name)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("must-gather CLI test failures:\n  %s", strings.Join(errors, "\n  "))
	}
	return nil
}

func runTestCase(ctx context.Context, binary string, tc mustGatherCLITestCase) error {
	outputDir, err := os.MkdirTemp("", "must-gather-cli-e2e-"+tc.name+"-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(outputDir)

	args := []string{
		"must-gather", tc.subcommand,
		"--kusto", kustoCluster,
		"--region", kustoRegion,
		"--limit", "10",
		"--output-path", outputDir,
	}
	args = append(args, tc.extraArgs...)

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %w\noutput: %s", err, string(output))
	}

	for _, dir := range tc.expectDirs {
		dirPath := filepath.Join(outputDir, dir)
		if err := verifyDirHasLogFiles(dirPath); err != nil {
			return fmt.Errorf("directory %q verification failed: %w", dir, err)
		}
	}

	return nil
}

func verifyDirHasLogFiles(dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("cannot read directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
			return nil
		}
	}

	return fmt.Errorf("no .log files found in %s", dirPath)
}

// infraClusterNames derives SVC and MGMT cluster names from the BUILD_ID env var.
// Returns empty strings if BUILD_ID is not set.
func infraClusterNames() (svcCluster, mgmtCluster string) {
	buildID := os.Getenv("BUILD_ID")
	if buildID == "" {
		return "", ""
	}

	// regionShort = "j" + last 7 chars of BUILD_ID
	suffix := buildID
	if len(suffix) > 7 {
		suffix = suffix[len(suffix)-7:]
	}
	regionShort := "j" + suffix

	// naming convention from config/config.yaml lines 1468/1488
	svcCluster = fmt.Sprintf("prow-%s-svc", regionShort)
	mgmtCluster = fmt.Sprintf("prow-%s-mgmt-1", regionShort)
	return svcCluster, mgmtCluster
}

// VerifyMustGatherCLI creates a verifier that tests the hcpctl must-gather CLI
func VerifyMustGatherCLI(subscriptionID, resourceGroup string) mustGatherCLIVerifier {
	return mustGatherCLIVerifier{
		subscriptionID: subscriptionID,
		resourceGroup:  resourceGroup,
	}
}
