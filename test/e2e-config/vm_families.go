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

package e2econfig

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed e2e-vm-families.yaml
var vmFamiliesYAML []byte

// VMFamilyPolicy contains ordered Azure ResourceSKU.Family identifiers. A nil
// role is unconfigured; an explicitly empty role is invalid.
type VMFamilyPolicy struct {
	WorkerFamilies []string `yaml:"worker_families"`
	HelperFamilies []string `yaml:"helper_families"`
}

type vmFamilyEnvironment struct {
	Defaults VMFamilyPolicy            `yaml:"defaults"`
	Regions  map[string]VMFamilyPolicy `yaml:"regions"`
}

// VMFamilyConfig resolves environment defaults and per-role regional overrides.
type VMFamilyConfig struct {
	Version      int                            `yaml:"version"`
	Environments map[string]vmFamilyEnvironment `yaml:"environments"`
}

// ParseVMFamilyConfig strictly validates a versioned family policy document.
func ParseVMFamilyConfig(data []byte) (*VMFamilyConfig, error) {
	// Null is not omission: rejecting it avoids silently inheriting a default
	// when an author explicitly supplied an empty role.
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("VM family policy: %w", err)
	}
	var rejectNullRoles func(*yaml.Node) error
	rejectNullRoles = func(node *yaml.Node) error {
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				if (key.Value == "worker_families" || key.Value == "helper_families") && value.Tag == "!!null" {
					return fmt.Errorf("VM family policy: %s must be a nonempty list", key.Value)
				}
			}
		}
		for _, child := range node.Content {
			if err := rejectNullRoles(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := rejectNullRoles(&document); err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config VMFamilyConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("VM family policy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("VM family policy must contain exactly one YAML document")
	}
	if config.Version != 1 || config.Environments == nil {
		return nil, fmt.Errorf("VM family policy requires version: 1 and environments")
	}
	validate := func(scope string, policy VMFamilyPolicy) error {
		for _, role := range [2]struct {
			name     string
			families []string
		}{
			{"worker_families", policy.WorkerFamilies},
			{"helper_families", policy.HelperFamilies},
		} {
			families := role.families
			if families == nil {
				continue
			}
			if len(families) == 0 {
				return fmt.Errorf("VM family policy %s %s must be nonempty", scope, role.name)
			}
			seen := make(map[string]bool, len(families))
			for _, family := range families {
				if strings.TrimSpace(family) != family || family == "" || seen[family] {
					return fmt.Errorf("VM family policy %s %s has empty, whitespace-padded or duplicate family %q", scope, role.name, family)
				}
				seen[family] = true
			}
		}
		return nil
	}
	for name, environment := range config.Environments {
		if name == "" || strings.TrimSpace(name) != name {
			return nil, fmt.Errorf("VM family policy has invalid environment %q", name)
		}
		if err := validate(name+" defaults", environment.Defaults); err != nil {
			return nil, err
		}
		regions := make(map[string]VMFamilyPolicy, len(environment.Regions))
		for region, policy := range environment.Regions {
			key := strings.ToLower(region)
			if key == "" || strings.TrimSpace(key) != key {
				return nil, fmt.Errorf("VM family policy has invalid region %q", region)
			}
			if _, exists := regions[key]; exists {
				return nil, fmt.Errorf("VM family policy has duplicate case-insensitive region %q", region)
			}
			if err := validate(name+"/"+region, policy); err != nil {
				return nil, err
			}
			regions[key] = policy
		}
		environment.Regions = regions
		config.Environments[name] = environment
	}
	return &config, nil
}

// Resolve replaces only roles supplied by the regional policy. Returned lists
// belong to the config and must not be modified.
func (config *VMFamilyConfig) Resolve(environment, region string) VMFamilyPolicy {
	env := config.Environments[environment]
	policy := env.Defaults
	if override, ok := env.Regions[strings.ToLower(region)]; ok {
		if override.WorkerFamilies != nil {
			policy.WorkerFamilies = override.WorkerFamilies
		}
		if override.HelperFamilies != nil {
			policy.HelperFamilies = override.HelperFamilies
		}
	}
	return policy
}

var embeddedVMFamilyConfig = sync.OnceValues(func() (*VMFamilyConfig, error) {
	return ParseVMFamilyConfig(vmFamiliesYAML)
})

// VMFamilies resolves the embedded policy, parsed once per binary. Invalid
// embedded configuration is a build/configuration defect, not SKU unavailability.
func VMFamilies(environment, region string) VMFamilyPolicy {
	config, err := embeddedVMFamilyConfig()
	if err != nil {
		panic(fmt.Errorf("invalid embedded e2e-vm-families.yaml: %w", err))
	}
	return config.Resolve(environment, region)
}
