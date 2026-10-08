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
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	e2econfig "github.com/Azure/ARO-HCP/test/e2e-config"
)

// PoolVMFamilyPolicy holds pool defaults and per-role runtime-region overrides.
// Only the resolved VMFamilyPolicy is passed to the test suite.
type PoolVMFamilyPolicy struct {
	e2econfig.VMFamilyPolicy `yaml:",inline"`
	Regions                  map[string]e2econfig.VMFamilyPolicy `yaml:"regions,omitempty"`
}

func (p *PoolVMFamilyPolicy) UnmarshalYAML(node *yaml.Node) error {
	fields, err := policyMapping(node)
	if err != nil {
		return err
	}
	roles := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	*p = PoolVMFamilyPolicy{}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "regions" {
			roles.Content = append(roles.Content, node.Content[i], node.Content[i+1])
		}
	}
	p.VMFamilyPolicy, err = decodeVMFamilyRoles(roles)
	if err != nil {
		return err
	}
	if regions, ok := fields["regions"]; ok {
		entries, err := policyMapping(regions)
		if err != nil {
			return fmt.Errorf("regions: %w", err)
		}
		p.Regions = make(map[string]e2econfig.VMFamilyPolicy, len(entries))
		for region, value := range entries {
			policy, err := decodeVMFamilyRoles(value)
			if err != nil {
				return fmt.Errorf("region %q: %w", region, err)
			}
			p.Regions[region] = policy
		}
	}
	return p.Validate()
}

// policyMapping keeps custom YAML decoding as strict as the catalog decoder,
// including duplicate keys and null values that yaml would otherwise ignore.
func policyMapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("VM family policy must be a mapping")
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("VM family policy keys must be strings")
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("duplicate VM family policy key %q", key.Value)
		}
		fields[key.Value] = node.Content[i+1]
	}
	return fields, nil
}

func decodeVMFamilyRoles(node *yaml.Node) (e2econfig.VMFamilyPolicy, error) {
	fields, err := policyMapping(node)
	if err != nil {
		return e2econfig.VMFamilyPolicy{}, err
	}
	for role, value := range fields {
		if role != "worker_families" && role != "helper_families" {
			return e2econfig.VMFamilyPolicy{}, fmt.Errorf("unknown VM family policy field %q", role)
		}
		if value.Kind != yaml.SequenceNode {
			return e2econfig.VMFamilyPolicy{}, fmt.Errorf("%s must be a non-empty sequence of strings", role)
		}
		for _, family := range value.Content {
			if family.Kind != yaml.ScalarNode || family.Tag != "!!str" {
				return e2econfig.VMFamilyPolicy{}, fmt.Errorf("%s entries must be strings", role)
			}
		}
	}
	var policy e2econfig.VMFamilyPolicy
	if err := node.Decode(&policy); err != nil {
		return policy, err
	}
	return policy, policy.Validate()
}

var policyRegionName = regexp.MustCompile(`^[a-z0-9]+$`)

func (p PoolVMFamilyPolicy) Validate() error {
	if err := p.VMFamilyPolicy.Validate(); err != nil {
		return err
	}
	for region, policy := range p.Regions {
		if !policyRegionName.MatchString(region) {
			return fmt.Errorf("invalid VM family policy region %q: use a lowercase Azure region name", region)
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("region %q: %w", region, err)
		}
	}
	return nil
}

func (p PoolVMFamilyPolicy) Resolve(runtimeRegion string) e2econfig.VMFamilyPolicy {
	resolved := p.VMFamilyPolicy
	if override, ok := p.Regions[strings.ToLower(runtimeRegion)]; ok {
		if override.WorkerFamilies != nil {
			resolved.WorkerFamilies = override.WorkerFamilies
		}
		if override.HelperFamilies != nil {
			resolved.HelperFamilies = override.HelperFamilies
		}
	}
	return resolved
}
