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
	"net/url"
	"regexp"
	"regexp/syntax"
	"strings"
	"time"
)

// Validate validates certificate cleanup policy sections.
func (p *CertificatePolicy) Validate(fieldPrefix string) error {
	vaultURL, err := normalizeVaultURL(p.VaultURL)
	if err != nil {
		return fmt.Errorf("%s.vaultURL: %w", fieldPrefix, err)
	}
	p.VaultURL = vaultURL
	if err := p.Discovery.Validate(fieldPrefix + ".discovery"); err != nil {
		return err
	}
	return p.OwnerInventory.validate(fieldPrefix+".ownerInventory", p.Discovery.RequiresOwnerInventory())
}

func normalizeVaultURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("must be an https URL with only a host, got %q", raw)
	}
	return "https://" + u.Host, nil
}

func (o *CertificateOwnerInventory) validate(fieldPrefix string, required bool) error {
	if required && len(o.Subscriptions) == 0 {
		return fmt.Errorf("%s.subscriptions: must name at least one subscription when a rule requires the owner resource group to be absent", fieldPrefix)
	}
	seen := make(map[string]bool, len(o.Subscriptions))
	for idx, name := range o.Subscriptions {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("%s.subscriptions[%d]: cannot be empty", fieldPrefix, idx)
		}
		if seen[name] {
			return fmt.Errorf("%s.subscriptions[%d]: duplicate subscription %q", fieldPrefix, idx, name)
		}
		seen[name] = true
		o.Subscriptions[idx] = name
	}
	return nil
}

// Validate validates certificate discovery configuration.
func (p *CertificateDiscoveryPolicy) Validate(fieldPrefix string) error {
	if len(p.ProtectedTags) == 0 {
		return fmt.Errorf("%s.protectedTags: must define at least one protected tag", fieldPrefix)
	}
	// Keys that differ only in case or whitespace would collapse into one entry
	// in an unspecified order, so which value protects would be arbitrary.
	seenKeys := make(map[string]string, len(p.ProtectedTags))
	for key := range p.ProtectedTags {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if other, duplicate := seenKeys[normalized]; duplicate && normalized != "" {
			first, second := other, key
			if first > second {
				first, second = second, first
			}
			return fmt.Errorf("%s.protectedTags: keys %q and %q name the same tag", fieldPrefix, first, second)
		}
		seenKeys[normalized] = key
	}
	protectedTags, err := normalizeTagMap(p.ProtectedTags, fieldPrefix+".protectedTags")
	if err != nil {
		return err
	}
	for key, value := range protectedTags {
		if value == "" {
			return fmt.Errorf("%s.protectedTags.%s: value is required", fieldPrefix, key)
		}
	}
	p.ProtectedTags = protectedTags
	if len(p.Rules) == 0 {
		return fmt.Errorf("%s.rules: must define at least one discovery rule", fieldPrefix)
	}
	for idx := range p.Rules {
		ruleFieldPrefix := fmt.Sprintf("%s.rules[%d]", fieldPrefix, idx)
		if err := p.Rules[idx].Validate(ruleFieldPrefix); err != nil {
			return err
		}
	}
	return nil
}

// Validate validates one certificate discovery rule.
func (r *CertificateDiscoveryRule) Validate(fieldPrefix string) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return fmt.Errorf("%s.name: is required", fieldPrefix)
	}
	if err := r.Match.Validate(fieldPrefix + ".match"); err != nil {
		return err
	}
	if r.Conditions.OwnerResourceGroupAbsent && r.Match.OwnerCaptureGroup == "" {
		return fmt.Errorf("%s.conditions.ownerResourceGroupAbsent: requires match.ownerCaptureGroup", fieldPrefix)
	}

	switch r.Action {
	case CertificateDiscoveryActionDelete:
		if r.OlderThan < 24*time.Hour {
			return fmt.Errorf("%s.olderThan: must be at least 24h for delete action", fieldPrefix)
		}
		switch {
		case r.Conditions.OwnerResourceGroupAbsent && r.AllowWithoutOwnerGuard:
			return fmt.Errorf("%s.allowWithoutOwnerGuard: cannot be combined with conditions.ownerResourceGroupAbsent", fieldPrefix)
		case !r.Conditions.OwnerResourceGroupAbsent && !r.AllowWithoutOwnerGuard:
			return fmt.Errorf("%s: delete rules must set conditions.ownerResourceGroupAbsent or explicitly opt out with allowWithoutOwnerGuard", fieldPrefix)
		case r.AllowWithoutOwnerGuard && r.Match.OwnerCaptureGroup != "":
			return fmt.Errorf("%s.match.ownerCaptureGroup: must be omitted when allowWithoutOwnerGuard is set", fieldPrefix)
		}
	case CertificateDiscoveryActionSkip:
		if r.OlderThan > 0 {
			return fmt.Errorf("%s.olderThan: must be omitted for skip action", fieldPrefix)
		}
		if r.Conditions.OwnerResourceGroupAbsent {
			return fmt.Errorf("%s.conditions.ownerResourceGroupAbsent: must be omitted for skip action", fieldPrefix)
		}
		if r.AllowWithoutOwnerGuard {
			return fmt.Errorf("%s.allowWithoutOwnerGuard: must be omitted for skip action", fieldPrefix)
		}
	default:
		return fmt.Errorf("%s.action: must be one of %q or %q", fieldPrefix, CertificateDiscoveryActionDelete, CertificateDiscoveryActionSkip)
	}
	return nil
}

// matchesWholeName reports whether every match of the pattern necessarily starts
// at the beginning and ends at the end of the text. Checking only the first and
// last character of the pattern is not enough: "^a|b$" has both anchors yet also
// matches names such as "a-production" and "production-b".
func matchesWholeName(re *regexp.Regexp) bool {
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return false
	}
	return startsAtBeginning(parsed) && endsAtEnd(parsed)
}

func startsAtBeginning(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText:
		return true
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if sub.Op == syntax.OpEmptyMatch {
				continue
			}
			return startsAtBeginning(sub)
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !startsAtBeginning(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	case syntax.OpCapture:
		return startsAtBeginning(re.Sub[0])
	}
	return false
}

func endsAtEnd(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEndText:
		return true
	case syntax.OpConcat:
		for i := len(re.Sub) - 1; i >= 0; i-- {
			if re.Sub[i].Op == syntax.OpEmptyMatch {
				continue
			}
			return endsAtEnd(re.Sub[i])
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !endsAtEnd(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	case syntax.OpCapture:
		return endsAtEnd(re.Sub[0])
	}
	return false
}

// Validate validates a certificate name matcher.
func (m *CertificateDiscoveryMatch) Validate(fieldPrefix string) error {
	m.OwnerCaptureGroup = strings.TrimSpace(m.OwnerCaptureGroup)
	if m.NameRegex == nil {
		return fmt.Errorf("%s.nameRegex: is required", fieldPrefix)
	}
	if !matchesWholeName(m.NameRegex) {
		return fmt.Errorf("%s.nameRegex: every alternative must be anchored with ^ and $ so the pattern always matches the whole name", fieldPrefix)
	}
	if m.OwnerCaptureGroup != "" && m.NameRegex.SubexpIndex(m.OwnerCaptureGroup) < 0 {
		return fmt.Errorf("%s.ownerCaptureGroup: named capture %q is not present in nameRegex", fieldPrefix, m.OwnerCaptureGroup)
	}
	return nil
}
