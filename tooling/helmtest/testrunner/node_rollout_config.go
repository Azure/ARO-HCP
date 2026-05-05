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
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

var knownSafeFlags = map[string]bool{
	"--enable-conversion-webhook":         true,
	"--managed-service":                   true,
	"--aro-hcp-key-vault-users-client-id": true,
	"--platform-monitoring":               true,
	"--metrics-set":                       true,
	"--enable-size-tagging":               true,
	"--limit-crd-install":                 true,
}

var knownSafeEnvVars = map[string]bool{
	"SHARED_INGRESS_AZURE_PIP_IP_TAGS": true,
}

type NodeRolloutConfig struct {
	RegistryOverrides         []string `json:"registryOverrides" yaml:"registryOverrides"`
	HypershiftImage           string   `json:"hypershiftImage" yaml:"hypershiftImage"`
	SharedIngressHAProxyImage string   `json:"sharedIngressHAProxyImage,omitempty" yaml:"sharedIngressHAProxyImage,omitempty"`
	AdditionalInstallArgs     []string `json:"additionalInstallArgs,omitempty" yaml:"additionalInstallArgs,omitempty"`
}

const nodeRolloutWarningHeader = `# WARNING: Changes to this file indicate HyperShift operator config changes
# that WILL trigger customer worker node replacements across all hosted
# clusters in affected regions.
#
# Before updating:
# 1. Document impact analysis in your PR description
# 2. Coordinate rollout scheduling with SRE
# 3. Run: UPDATE=true make -C tooling/helmtest update
`

const nodeRolloutDiffMessage = `Node rollout configuration has changed!

Changes to these values will trigger customer worker node replacement
across all hosted clusters in affected regions.

Before updating, document the impact in your PR description and coordinate with SRE.
If this change is intentional, re-run with: UPDATE=true make -C tooling/helmtest update`

func RunTestNodeRolloutConfig(t *testing.T, settingsPath string) {
	settings, err := internal.LoadSettings(settingsPath)
	assert.NoError(t, err)
	assert.NotNil(t, settings)

	helmSteps, err := internal.FindHelmSteps(settings.TopologyDir, settings.ConfigPath)
	assert.NoError(t, err)
	assert.NotNil(t, helmSteps)

	for _, helmStep := range helmSteps {
		if !strings.Contains(helmStep.PipelinePath, "hypershiftoperator/") {
			continue
		}

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

			configYAML, err := yaml.Marshal(config)
			if err != nil {
				t.Fatalf("failed to marshal node rollout config: %v", err)
			}

			output := nodeRolloutWarningHeader + string(configYAML)
			outputDir := filepath.Join(settings.TopologyDir, filepath.Dir(helmStep.PipelinePath))
			CompareWithFixture(t, output,
				WithGoldenDir(outputDir),
				WithDiffMessage(nodeRolloutDiffMessage),
			)
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

	if image, ok := extractFlag(script, "hypershift-image"); ok {
		config.HypershiftImage = image
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
	docs := strings.Split(manifest, "\n---\n")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		if !strings.Contains(doc, "kind: Job") || !strings.Contains(doc, "name: install-hypershift") {
			continue
		}

		var job struct {
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Command []string `yaml:"command"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(doc), &job); err != nil {
			return "", fmt.Errorf("failed to parse install-hypershift Job: %w", err)
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

	knownNodeAffecting := map[string]bool{
		"--registry-overrides": true,
		"--hypershift-image":   true,
	}

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
			if envName == "IMAGE_SHARED_INGRESS_HAPROXY" {
				continue
			}
			if knownSafeEnvVars[envName] {
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

		if knownSafeFlags[flagName] {
			continue
		}
		if knownNodeAffecting[flagName] {
			continue
		}

		unknown = append(unknown, line)
	}

	return unknown
}
