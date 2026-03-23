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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Policy struct {
	RGOrdered RGOrderedPolicy `json:"rgOrdered" yaml:"rgOrdered"`
}

type RGOrderedPolicy struct {
	ExcludedResourceGroups []string          `json:"excludedResourceGroups" yaml:"excludedResourceGroups"`
	Discovery              RGDiscoveryPolicy `json:"discovery" yaml:"discovery"`
}

type RGDiscoveryPolicy struct {
	Rules []RGDiscoveryRule `json:"rules" yaml:"rules"`
}

type RGDiscoveryAction string

const (
	RGDiscoveryActionDelete RGDiscoveryAction = "delete"
	RGDiscoveryActionSkip   RGDiscoveryAction = "skip"
)

type RGDiscoveryRule struct {
	Name       string                `json:"name,omitempty" yaml:"name,omitempty"`
	Action     RGDiscoveryAction     `json:"action" yaml:"action"`
	Match      RGDiscoveryMatch      `json:"match" yaml:"match"`
	Conditions RGDiscoveryConditions `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	OlderThan  time.Duration         `json:"olderThan,omitempty" yaml:"olderThan,omitempty"`
}

type RGDiscoveryMatch struct {
	Any        bool           `json:"any,omitempty" yaml:"any,omitempty"`
	NamePrefix string         `json:"namePrefix,omitempty" yaml:"namePrefix,omitempty"`
	NameRegex  *regexp.Regexp `json:"nameRegex,omitempty" yaml:"nameRegex,omitempty"`
}

type RGDiscoveryConditions struct {
	ManagedBySet *bool             `json:"managedBySet,omitempty" yaml:"managedBySet,omitempty"`
	TagsEq       map[string]string `json:"tagsEq,omitempty" yaml:"tagsEq,omitempty"`
	TagsNotEq    map[string]string `json:"tagsNotEq,omitempty" yaml:"tagsNotEq,omitempty"`
}

type rgDiscoveryRuleWireFormat struct {
	Name       string                `json:"name,omitempty"`
	Action     RGDiscoveryAction     `json:"action"`
	Match      RGDiscoveryMatch      `json:"match"`
	Conditions RGDiscoveryConditions `json:"conditions,omitempty"`
	OlderThan  string                `json:"olderThan,omitempty"`
}

type rgDiscoveryMatchWireFormat struct {
	Any        bool   `json:"any,omitempty"`
	NamePrefix string `json:"namePrefix,omitempty"`
	NameRegex  string `json:"nameRegex,omitempty"`
}

func (r RGDiscoveryRule) MarshalJSON() ([]byte, error) {
	wire := rgDiscoveryRuleWireFormat{
		Name:       r.Name,
		Action:     r.Action,
		Match:      r.Match,
		Conditions: r.Conditions,
	}
	if r.OlderThan > 0 {
		wire.OlderThan = r.OlderThan.String()
	}
	return json.Marshal(wire)
}

func (r *RGDiscoveryRule) UnmarshalJSON(data []byte) error {
	var wire rgDiscoveryRuleWireFormat
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	r.Name = strings.TrimSpace(wire.Name)
	r.Action = wire.Action
	r.Match = wire.Match
	r.Conditions = wire.Conditions
	r.OlderThan = 0

	olderThan := strings.TrimSpace(wire.OlderThan)
	if olderThan == "" {
		return nil
	}
	parsedDuration, err := time.ParseDuration(olderThan)
	if err != nil {
		return fmt.Errorf("olderThan: invalid duration %q: %w", wire.OlderThan, err)
	}
	r.OlderThan = parsedDuration
	return nil
}

func (m RGDiscoveryMatch) MarshalJSON() ([]byte, error) {
	wire := rgDiscoveryMatchWireFormat{
		Any:        m.Any,
		NamePrefix: m.NamePrefix,
	}
	if m.NameRegex != nil {
		wire.NameRegex = m.NameRegex.String()
	}
	return json.Marshal(wire)
}

func (m *RGDiscoveryMatch) UnmarshalJSON(data []byte) error {
	var wire rgDiscoveryMatchWireFormat
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	m.Any = wire.Any
	m.NamePrefix = strings.TrimSpace(wire.NamePrefix)
	m.NameRegex = nil

	nameRegex := strings.TrimSpace(wire.NameRegex)
	if nameRegex == "" {
		return nil
	}
	compiledRegex, err := regexp.Compile(nameRegex)
	if err != nil {
		return fmt.Errorf("nameRegex: invalid regex %q: %w", wire.NameRegex, err)
	}
	m.NameRegex = compiledRegex
	return nil
}
