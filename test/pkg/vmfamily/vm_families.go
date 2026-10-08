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

package vmfamily

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// VMFamilyPolicy contains ordered Azure ResourceSKU.Family identifiers. A nil
// role is unconfigured; an explicitly empty role is invalid.
type VMFamilyPolicy struct {
	WorkerFamilies []string `yaml:"worker_families,omitempty" json:"worker_families,omitempty"`
	HelperFamilies []string `yaml:"helper_families,omitempty" json:"helper_families,omitempty"`
}

// Validate checks configured roles without modifying their family order.
func (policy VMFamilyPolicy) Validate() error {
	for _, role := range [2]struct {
		name     string
		families []string
	}{
		{"worker_families", policy.WorkerFamilies},
		{"helper_families", policy.HelperFamilies},
	} {
		if role.families == nil {
			continue
		}
		if len(role.families) == 0 {
			return fmt.Errorf("VM family policy %s must be a nonempty list", role.name)
		}
		seen := make(map[string]bool, len(role.families))
		for _, family := range role.families {
			if strings.TrimSpace(family) != family || family == "" || seen[family] {
				return fmt.Errorf("VM family policy %s has empty, whitespace-padded or duplicate family %q", role.name, family)
			}
			seen[family] = true
		}
	}
	return nil
}

// ParseVMFamilyPolicy strictly parses one resolved JSON object. Omitted roles
// preserve historical selection; null roles, unknown or duplicate keys, and
// trailing documents are rejected rather than silently discarding policy.
func ParseVMFamilyPolicy(data []byte) (VMFamilyPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return VMFamilyPolicy{}, fmt.Errorf("VM family policy: %w", err)
	}
	if opening != json.Delim('{') {
		return VMFamilyPolicy{}, fmt.Errorf("VM family policy must be a JSON object")
	}
	var policy VMFamilyPolicy
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return VMFamilyPolicy{}, fmt.Errorf("VM family policy: %w", err)
		}
		var families *[]string
		switch key {
		case "worker_families":
			families = &policy.WorkerFamilies
		case "helper_families":
			families = &policy.HelperFamilies
		default:
			return VMFamilyPolicy{}, fmt.Errorf("VM family policy has unknown key %q", key)
		}
		if *families != nil {
			return VMFamilyPolicy{}, fmt.Errorf("VM family policy has duplicate key %q", key)
		}
		if err := decoder.Decode(families); err != nil {
			return VMFamilyPolicy{}, fmt.Errorf("VM family policy %s: %w", key, err)
		}
		if *families == nil {
			return VMFamilyPolicy{}, fmt.Errorf("VM family policy %s must be a nonempty list, not null", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return VMFamilyPolicy{}, fmt.Errorf("VM family policy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return VMFamilyPolicy{}, fmt.Errorf("VM family policy must contain exactly one JSON object")
	}
	if err := policy.Validate(); err != nil {
		return VMFamilyPolicy{}, err
	}
	return policy, nil
}
