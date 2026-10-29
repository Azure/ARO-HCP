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

// CertificatePolicy configures which vault is swept, which resource groups
// count as certificate owners, and which certificates are cleaned up.
type CertificatePolicy struct {
	// VaultURL is the only vault the sweep reads and mutates.
	VaultURL string `json:"vaultURL" yaml:"vaultURL"`
	// OwnerInventory names the subscriptions whose resource groups decide whether
	// an owner is still alive. Required when a rule uses ownerResourceGroupAbsent.
	OwnerInventory CertificateOwnerInventory  `json:"ownerInventory,omitempty" yaml:"ownerInventory,omitempty"`
	Discovery      CertificateDiscoveryPolicy `json:"discovery" yaml:"discovery"`
}

// CertificateOwnerInventory configures the resource-group owner inventory.
type CertificateOwnerInventory struct {
	// Subscriptions are subscription display names, resolved to IDs at run time
	// so that IDs stay out of the repository.
	Subscriptions []string `json:"subscriptions,omitempty" yaml:"subscriptions,omitempty"`
}

// CertificateDiscoveryPolicy contains the protected tags and the first-match-wins
// certificate rules.
type CertificateDiscoveryPolicy struct {
	// ProtectedTags is a mandatory veto evaluated before any rule: a certificate
	// carrying any of these tags (key and value compared case-insensitively) is
	// never selected, whatever rule it would match.
	ProtectedTags map[string]string          `json:"protectedTags" yaml:"protectedTags"`
	Rules         []CertificateDiscoveryRule `json:"rules" yaml:"rules"`
}

// CertificateDiscoveryAction is the action a certificate rule takes on match.
type CertificateDiscoveryAction string

const (
	// CertificateDiscoveryActionDelete marks matching certificates for deletion.
	CertificateDiscoveryActionDelete CertificateDiscoveryAction = "delete"
	// CertificateDiscoveryActionSkip excludes matching certificates from deletion.
	CertificateDiscoveryActionSkip CertificateDiscoveryAction = "skip"
)

// CertificateDiscoveryRule defines one ordered certificate discovery rule.
type CertificateDiscoveryRule struct {
	Name       string                         `json:"name,omitempty" yaml:"name,omitempty"`
	Action     CertificateDiscoveryAction     `json:"action" yaml:"action"`
	Match      CertificateDiscoveryMatch      `json:"match" yaml:"match"`
	Conditions CertificateDiscoveryConditions `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	OlderThan  time.Duration                  `json:"olderThan,omitempty" yaml:"olderThan,omitempty"`
	// AllowWithoutOwnerGuard is the explicit opt-out from the mandatory owner
	// guard. Every delete rule must either set conditions.ownerResourceGroupAbsent
	// or set this field, so an unguarded rule is always a deliberate choice.
	AllowWithoutOwnerGuard bool `json:"allowWithoutOwnerGuard,omitempty" yaml:"allowWithoutOwnerGuard,omitempty"`
}

// CertificateDiscoveryMatch matches a certificate name and optionally extracts
// the resource-group owner token from a named regular-expression capture.
type CertificateDiscoveryMatch struct {
	NameRegex         *regexp.Regexp `json:"nameRegex" yaml:"nameRegex"`
	OwnerCaptureGroup string         `json:"ownerCaptureGroup,omitempty" yaml:"ownerCaptureGroup,omitempty"`
}

// CertificateDiscoveryConditions defines additional safety predicates.
type CertificateDiscoveryConditions struct {
	OwnerResourceGroupAbsent bool `json:"ownerResourceGroupAbsent,omitempty" yaml:"ownerResourceGroupAbsent,omitempty"`
}

// RequiresOwnerInventory reports whether any rule needs resource-group ownership data.
func (p *CertificateDiscoveryPolicy) RequiresOwnerInventory() bool {
	if p == nil {
		return false
	}
	for _, rule := range p.Rules {
		if rule.Conditions.OwnerResourceGroupAbsent {
			return true
		}
	}
	return false
}

type certificateDiscoveryRuleWireFormat struct {
	Name       string                         `json:"name,omitempty"`
	Action     CertificateDiscoveryAction     `json:"action"`
	Match      CertificateDiscoveryMatch      `json:"match"`
	Conditions CertificateDiscoveryConditions `json:"conditions,omitempty"`
	OlderThan  string                         `json:"olderThan,omitempty"`

	AllowWithoutOwnerGuard bool `json:"allowWithoutOwnerGuard,omitempty"`
}

type certificateDiscoveryMatchWireFormat struct {
	NameRegex         string `json:"nameRegex"`
	OwnerCaptureGroup string `json:"ownerCaptureGroup,omitempty"`
}

// MarshalJSON serializes CertificateDiscoveryRule using duration strings.
func (r CertificateDiscoveryRule) MarshalJSON() ([]byte, error) {
	wire := certificateDiscoveryRuleWireFormat{
		Name:       r.Name,
		Action:     r.Action,
		Match:      r.Match,
		Conditions: r.Conditions,

		AllowWithoutOwnerGuard: r.AllowWithoutOwnerGuard,
	}
	if r.OlderThan > 0 {
		wire.OlderThan = r.OlderThan.String()
	}
	return json.Marshal(wire)
}

// UnmarshalJSON parses CertificateDiscoveryRule from wire format.
func (r *CertificateDiscoveryRule) UnmarshalJSON(data []byte) error {
	var wire certificateDiscoveryRuleWireFormat
	if err := unmarshalStrict(data, &wire); err != nil {
		return err
	}

	r.Name = strings.TrimSpace(wire.Name)
	r.Action = wire.Action
	r.Match = wire.Match
	r.Conditions = wire.Conditions
	r.AllowWithoutOwnerGuard = wire.AllowWithoutOwnerGuard
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

// MarshalJSON serializes CertificateDiscoveryMatch with a regex string.
func (m CertificateDiscoveryMatch) MarshalJSON() ([]byte, error) {
	wire := certificateDiscoveryMatchWireFormat{
		OwnerCaptureGroup: m.OwnerCaptureGroup,
	}
	if m.NameRegex != nil {
		wire.NameRegex = m.NameRegex.String()
	}
	return json.Marshal(wire)
}

// UnmarshalJSON parses CertificateDiscoveryMatch from wire format.
func (m *CertificateDiscoveryMatch) UnmarshalJSON(data []byte) error {
	var wire certificateDiscoveryMatchWireFormat
	if err := unmarshalStrict(data, &wire); err != nil {
		return err
	}

	m.NameRegex = nil
	m.OwnerCaptureGroup = strings.TrimSpace(wire.OwnerCaptureGroup)

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
