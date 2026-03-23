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

package policy

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

func (p *Policy) Validate() error {
	if p == nil {
		return fmt.Errorf("policy is required")
	}
	if err := p.RGOrdered.Validate("rgOrdered"); err != nil {
		return err
	}
	return nil
}

func (p *RGOrderedPolicy) Validate(fieldPrefix string) error {
	p.ExcludedResourceGroups = normalizeStringSetLower(p.ExcludedResourceGroups)
	return p.Discovery.Validate(fieldPrefix + ".discovery")
}

func (p *RGDiscoveryPolicy) Validate(fieldPrefix string) error {
	for idx := range p.Rules {
		ruleFieldPrefix := fmt.Sprintf("%s.rules[%d]", fieldPrefix, idx)
		if err := p.Rules[idx].Validate(ruleFieldPrefix); err != nil {
			return err
		}
	}
	return nil
}

func (r *RGDiscoveryRule) Validate(fieldPrefix string) error {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.Match.Validate(fieldPrefix + ".match"); err != nil {
		return err
	}
	if err := r.Conditions.Validate(fieldPrefix + ".conditions"); err != nil {
		return err
	}

	switch r.Action {
	case RGDiscoveryActionDelete:
		if r.OlderThan <= 0 {
			return fmt.Errorf("%s.olderThan: must be > 0 for delete action", fieldPrefix)
		}
	case RGDiscoveryActionSkip:
		if r.OlderThan > 0 {
			return fmt.Errorf("%s.olderThan: must be omitted for skip action", fieldPrefix)
		}
	default:
		return fmt.Errorf("%s.action: must be one of %q or %q", fieldPrefix, RGDiscoveryActionDelete, RGDiscoveryActionSkip)
	}

	return nil
}

func (m *RGDiscoveryMatch) Validate(fieldPrefix string) error {
	m.NamePrefix = strings.TrimSpace(m.NamePrefix)

	matchFields := 0
	if m.Any {
		matchFields++
	}
	if m.NamePrefix != "" {
		matchFields++
	}
	if m.NameRegex != nil {
		matchFields++
	}
	if matchFields != 1 {
		return fmt.Errorf("%s: must define exactly one of any, namePrefix, or nameRegex", fieldPrefix)
	}
	return nil
}

func (c *RGDiscoveryConditions) Validate(fieldPrefix string) error {
	tagsEq, err := normalizeTagMap(c.TagsEq, fieldPrefix+".tagsEq")
	if err != nil {
		return err
	}
	c.TagsEq = tagsEq

	tagsNotEq, err := normalizeTagMap(c.TagsNotEq, fieldPrefix+".tagsNotEq")
	if err != nil {
		return err
	}
	c.TagsNotEq = tagsNotEq
	return nil
}

func normalizeTagMap(values map[string]string, fieldPrefix string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	normalized := make(map[string]string, len(values))
	for key, value := range values {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return nil, fmt.Errorf("%s: cannot contain empty keys", fieldPrefix)
		}
		normalized[strings.ToLower(normalizedKey)] = strings.TrimSpace(value)
	}
	return normalized, nil
}

func normalizeStringSetLower(values []string) []string {
	valueSet := sets.New[string]()
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		valueSet.Insert(trimmed)
	}
	return sets.List(valueSet)
}
