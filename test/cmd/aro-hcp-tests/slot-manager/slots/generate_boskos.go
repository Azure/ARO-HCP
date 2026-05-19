// Copyright 2026 Microsoft Corporation
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

package slots

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	BoskosTypesBeginMarker     = "    # BEGIN ARO-HCP E2E SLOT TYPES"
	BoskosTypesEndMarker       = "    # END ARO-HCP E2E SLOT TYPES"
	BoskosResourcesBeginMarker = "# BEGIN ARO-HCP E2E SLOT RESOURCES"
	BoskosResourcesEndMarker   = "# END ARO-HCP E2E SLOT RESOURCES"
)

type BoskosConfig struct {
	Resources []BoskosResource `yaml:"resources"`
}

type BoskosResource struct {
	Type     string   `yaml:"type"`
	Names    []string `yaml:"names,omitempty"`
	MinCount *int     `yaml:"min-count,omitempty"`
	MaxCount *int     `yaml:"max-count,omitempty"`
}

func GenerateBoskosPythonPath(releaseRepo string) string {
	return filepath.Join(releaseRepo, "core-services", "prow", "02_config", "generate-boskos.py")
}

func BoskosYAMLPath(releaseRepo string) string {
	return filepath.Join(releaseRepo, "core-services", "prow", "02_config", "_boskos.yaml")
}

func RewriteGenerateBoskos(releaseRepo string, catalog *Catalog) error {
	generateBoskosPath := GenerateBoskosPythonPath(releaseRepo)
	data, err := os.ReadFile(generateBoskosPath)
	if err != nil {
		return fmt.Errorf("failed to read %q: %w", generateBoskosPath, err)
	}

	content := string(data)
	content, err = replaceMarkerBlock(content, BoskosTypesBeginMarker, BoskosTypesEndMarker, RenderBoskosTypesBlock(catalog))
	if err != nil {
		return err
	}
	content, err = replaceMarkerBlock(content, BoskosResourcesBeginMarker, BoskosResourcesEndMarker, RenderBoskosResourcesBlock(catalog))
	if err != nil {
		return err
	}

	if err := os.WriteFile(generateBoskosPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write %q: %w", generateBoskosPath, err)
	}
	return nil
}

func RenderBoskosTypesBlock(catalog *Catalog) string {
	lines := []string{BoskosTypesBeginMarker}
	for _, environmentName := range catalog.EnvironmentNames() {
		for _, pool := range catalog.Environments[environmentName].Pools {
			lines = append(lines, fmt.Sprintf("    '%s': {},", pool.ResourceType))
		}
	}
	lines = append(lines, BoskosTypesEndMarker)
	return strings.Join(lines, "\n")
}

func RenderBoskosResourcesBlock(catalog *Catalog) string {
	lines := []string{BoskosResourcesBeginMarker}
	for _, environmentName := range catalog.EnvironmentNames() {
		for _, pool := range catalog.Environments[environmentName].Pools {
			lines = append(lines, fmt.Sprintf("for i in range(%d):", pool.SlotCount))
			lines = append(lines, fmt.Sprintf("    CONFIG['%s']['%s-{i:0>2}'.format(i=i)] = 1", pool.ResourceType, pool.ResourceType))
		}
	}
	lines = append(lines, BoskosResourcesEndMarker)
	return strings.Join(lines, "\n")
}

// ValidateBoskosConfig checks that the generated Boskos YAML in releaseRepo
// contains the resource types and names expected by the slot catalog. This
// check is intentionally informational: when growing a pool you must update
// the catalog first, then sync the Boskos config; when shrinking a pool the
// order is reversed. A strict presubmit gate would reject the first half of
// either change.
func ValidateBoskosConfig(releaseRepo string, catalog *Catalog) error {
	expected, err := ExpectedBoskosResources(catalog)
	if err != nil {
		return err
	}

	actualConfigPath := BoskosYAMLPath(releaseRepo)
	data, err := os.ReadFile(actualConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read %q: %w", actualConfigPath, err)
	}

	actual := &BoskosConfig{}
	if err := yaml.Unmarshal(data, actual); err != nil {
		return fmt.Errorf("failed to unmarshal %q: %w", actualConfigPath, err)
	}

	actualResources := map[string][]string{}
	for _, resource := range actual.Resources {
		if len(resource.Names) == 0 {
			continue
		}
		actualResources[resource.Type] = append([]string{}, resource.Names...)
		sort.Strings(actualResources[resource.Type])
	}

	for resourceType, expectedNames := range expected {
		actualNames, found := actualResources[resourceType]
		if !found {
			return fmt.Errorf("expected Boskos resource type %q in %s", resourceType, actualConfigPath)
		}
		if strings.Join(expectedNames, ",") != strings.Join(actualNames, ",") {
			return fmt.Errorf("resource type %q is out of sync with the slot catalog", resourceType)
		}
	}

	return nil
}

func ExpectedBoskosResources(catalog *Catalog) (map[string][]string, error) {
	expected := map[string][]string{}
	for _, environmentName := range catalog.EnvironmentNames() {
		slots, err := catalog.ExpandedSlotsForEnvironment(environmentName)
		if err != nil {
			return nil, err
		}
		for _, slot := range slots {
			expected[slot.ResourceType] = append(expected[slot.ResourceType], slot.ResourceName)
		}
	}

	for resourceType := range expected {
		sort.Strings(expected[resourceType])
	}

	return expected, nil
}

func replaceMarkerBlock(content, beginMarker, endMarker, replacement string) (string, error) {
	beginIndex := strings.Index(content, beginMarker)
	if beginIndex == -1 {
		return "", fmt.Errorf("failed to find begin marker %q", beginMarker)
	}
	endIndex := strings.Index(content, endMarker)
	if endIndex == -1 {
		return "", fmt.Errorf("failed to find end marker %q", endMarker)
	}
	if endIndex < beginIndex {
		return "", fmt.Errorf("marker order is invalid for %q and %q", beginMarker, endMarker)
	}

	endIndex += len(endMarker)
	return content[:beginIndex] + replacement + content[endIndex:], nil
}
