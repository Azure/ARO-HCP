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

package testrunner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	batchv1 "k8s.io/api/batch/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

// flagEffect classifies how a HyperShift operator install flag or environment
// variable affects worker node configuration.
type flagEffect int

const (
	// flagSafe indicates the flag does not affect worker node configuration.
	// It controls operator-level behavior only (webhooks, monitoring, CRD
	// management) and will be silently ignored by this test.
	flagSafe flagEffect = iota

	// flagNodeAffecting indicates the flag affects worker node configuration
	// and is tracked in a dedicated NodeRolloutConfig field. Changes to these
	// flags will appear in the golden fixture diff.
	flagNodeAffecting
)

// flagCategories is the single source of truth for classifying HyperShift
// operator install flags that are known to be safe or node-affecting. Every
// flag passed to `hypershift install` should either be classified here or be
// intentionally left unclassified so it surfaces in AdditionalInstallArgs in
// the golden fixture for explicit review.
//
// To determine whether a flag is safe or node-affecting, review the HyperShift
// install command source code and verify:
//  1. The flag does not modify any NodePool, Machine, or MachineSet spec
//  2. The flag does not change container images used by worker nodes
//  3. The flag does not alter registry overrides or image pull configuration
//
// If uncertain, do NOT add the flag here -- leaving it unclassified causes the
// test to surface it for review, which is the safer default.
var flagCategories = map[string]flagEffect{
	// Safe: only affect operator-level behavior, no node impact
	"--enable-conversion-webhook":         flagSafe,
	"--managed-service":                   flagSafe,
	"--aro-hcp-key-vault-users-client-id": flagSafe,
	"--platform-monitoring":               flagSafe,
	"--enable-size-tagging":               flagSafe,
	"--limit-crd-install":                 flagSafe,
	"--hypershift-image":                  flagSafe,

	// Node-affecting: tracked in dedicated NodeRolloutConfig fields
	"--registry-overrides": flagNodeAffecting,
}

// envVarCategories is the single source of truth for classifying HyperShift
// operator environment variables set via --additional-operator-env-vars.
// Same classification rules as flagCategories.
var envVarCategories = map[string]flagEffect{
	// Safe: only affect operator-level behavior, no node impact
	"SHARED_INGRESS_AZURE_PIP_IP_TAGS": flagSafe,

	// Node-affecting: tracked in dedicated NodeRolloutConfig fields
	"IMAGE_SHARED_INGRESS_HAPROXY": flagNodeAffecting,
}

type NodeRolloutConfig struct {
	RegistryOverrides         []string `json:"registryOverrides" yaml:"registryOverrides"`
	SharedIngressHAProxyImage string   `json:"sharedIngressHAProxyImage,omitempty" yaml:"sharedIngressHAProxyImage,omitempty"`
	AdditionalInstallArgs     []string `json:"additionalInstallArgs,omitempty" yaml:"additionalInstallArgs,omitempty"`
}

const nodeRolloutWarningHeader = `# WARNING: Changes to this file indicate HyperShift operator install flag
# changes that WILL trigger customer worker node replacements across all
# hosted clusters in affected regions.
#
# Before updating:
# 1. Document impact analysis in your PR description
# 2. Coordinate rollout scheduling with SRE
# 3. Run: UPDATE_NODE_ROLLOUT_FIXTURE=true go test -run TestNodeRolloutConfig -count=1 ./...
`

const nodeRolloutDiffMessage = `Node rollout configuration has changed!

Changes to these values will trigger customer worker node replacement
across all hosted clusters in affected regions.

Before updating, document the impact in your PR description and coordinate with SRE.
If this change is intentional, re-run with: UPDATE_NODE_ROLLOUT_FIXTURE=true go test -run TestNodeRolloutConfig -count=1 ./...`

func RunTestNodeRolloutConfig(t *testing.T, settingsPath string) {
	settings, err := internal.LoadSettings(settingsPath)
	assert.NoError(t, err)
	assert.NotNil(t, settings)

	helmSteps, err := internal.FindHelmStepsByReleaseName(settings.TopologyDir, settings.ConfigPath, "hypershift")
	assert.NoError(t, err)
	if len(helmSteps) == 0 {
		t.Fatal("no HyperShift operator helm steps found; if the service was renamed or moved, update the release name filter in this test")
	}

	for _, helmStep := range helmSteps {
		testName := fmt.Sprintf("%s-%s", helmStep.AKSCluster, helmStep.HelmStep.ReleaseName)
		testCase := internal.TestCase{
			Name:         testName,
			Namespace:    helmStep.HelmStep.ReleaseNamespace,
			Values:       helmStep.ValuesFileFromRoot(settings.TopologyDir),
			HelmChartDir: helmStep.ChartDirFromRoot(settings.TopologyDir),
			TestData:     map[string]any{},
			Implicit:     true,
		}

		t.Run(testName, func(t *testing.T) {
			manifest, err := runTest(t.Context(), settings, testCase)
			assert.NoError(t, err)

			config, err := extractNodeRolloutConfig(manifest)
			if err != nil {
				t.Fatalf("failed to extract node rollout config: %v", err)
			}

			configYAML, err := sigsyaml.Marshal(config)
			if err != nil {
				t.Fatalf("failed to marshal node rollout config: %v", err)
			}

			output := nodeRolloutWarningHeader + string(configYAML)
			outputDir := filepath.Join(settings.TopologyDir, filepath.Dir(helmStep.PipelinePath))
			CompareWithFixture(t, output,
				WithGoldenDir(outputDir),
				WithUpdateEnv("UPDATE_NODE_ROLLOUT_FIXTURE"),
			)

			if t.Failed() {
				t.Log(nodeRolloutDiffMessage)
			}
		})
	}
}

func extractNodeRolloutConfig(manifest string) (*NodeRolloutConfig, error) {
	script, err := extractInstallScript(manifest)
	if err != nil {
		return nil, err
	}

	config := &NodeRolloutConfig{}

	if overrides, ok := extractFlag(script, "registry-overrides"); ok {
		entries := strings.Split(overrides, ",")
		sort.Strings(entries)
		config.RegistryOverrides = entries
	}

	if haproxy := extractEnvVar(script, "IMAGE_SHARED_INGRESS_HAPROXY"); haproxy != "" {
		config.SharedIngressHAProxyImage = haproxy
	}

	additional := collectUnknownFlags(script)
	if len(additional) > 0 {
		sort.Strings(additional)
		config.AdditionalInstallArgs = additional
	}

	return config, nil
}

func extractInstallScript(manifest string) (string, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(manifest)), 4096)
	for {
		var job batchv1.Job
		if err := decoder.Decode(&job); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		if job.Kind != "Job" || job.Name != "install-hypershift" {
			continue
		}
		if len(job.Spec.Template.Spec.Containers) == 0 {
			return "", fmt.Errorf("install-hypershift Job has no containers")
		}
		cmd := job.Spec.Template.Spec.Containers[0].Command
		if len(cmd) < 3 {
			return "", fmt.Errorf("install-hypershift Job command has fewer than 3 elements")
		}
		return cmd[2], nil
	}
	return "", fmt.Errorf("install-hypershift Job not found in rendered manifest")
}

func extractFlag(script, flagName string) (string, bool) {
	re := regexp.MustCompile(`--` + regexp.QuoteMeta(flagName) + `\s+"([^"]+)"`)
	if m := re.FindStringSubmatch(script); m != nil {
		return m[1], true
	}
	re = regexp.MustCompile(`--` + regexp.QuoteMeta(flagName) + `[=\s]+(\S+)`)
	if m := re.FindStringSubmatch(script); m != nil {
		val := strings.TrimRight(m[1], `\`)
		return val, true
	}
	return "", false
}

func extractEnvVar(script, envVarName string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(envVarName) + `=(\S+)`)
	if m := re.FindStringSubmatch(script); m != nil {
		val := strings.TrimRight(m[1], `\`)
		return val
	}
	return ""
}

func collectUnknownFlags(script string) []string {
	lines := strings.Split(script, "\n")
	var unknown []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, `\`)
		line = strings.TrimSpace(line)
		if line == "" || line == "hypershift install" {
			continue
		}

		if strings.HasPrefix(line, "--additional-operator-env-vars") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) < 2 {
				continue
			}
			envAssignment := strings.TrimSpace(parts[1])
			envName := strings.SplitN(envAssignment, "=", 2)[0]
			if _, classified := envVarCategories[envName]; classified {
				continue
			}
			unknown = append(unknown, line)
			continue
		}

		if !strings.HasPrefix(line, "--") {
			continue
		}

		flagName := line
		if idx := strings.IndexAny(line, "= "); idx > 0 {
			flagName = line[:idx]
		}

		if _, classified := flagCategories[flagName]; classified {
			continue
		}

		unknown = append(unknown, line)
	}

	return unknown
}
