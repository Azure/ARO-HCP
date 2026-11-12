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
	"strings"
	"time"
	"unicode"

	"k8s.io/apimachinery/pkg/util/sets"
)

// CertificateDecision is the outcome of evaluating one certificate against a
// certificate discovery policy: either selected for cleanup or the reason it
// was retained.
type CertificateDecision string

const (
	// CertificateDecisionSelected means a delete rule selected the certificate.
	CertificateDecisionSelected CertificateDecision = "selected"
	// CertificateDecisionInvalidID means the certificate has no usable name.
	CertificateDecisionInvalidID CertificateDecision = "invalid-id"
	// CertificateDecisionProtectedTag means a tag listed in protectedTags protects the certificate.
	CertificateDecisionProtectedTag CertificateDecision = "protected-tag"
	// CertificateDecisionNoRuleMatch means no rule matched; unmatched certificates are retained.
	CertificateDecisionNoRuleMatch CertificateDecision = "no-rule-match"
	// CertificateDecisionSkippedByRule means the first matching rule has the skip action.
	CertificateDecisionSkippedByRule CertificateDecision = "rule-skip"
	// CertificateDecisionMissingOwner means the owner capture group did not yield a token.
	CertificateDecisionMissingOwner CertificateDecision = "missing-owner"
	// CertificateDecisionInvalidOwner means the captured owner is not a single token, so
	// the resource-group owner inventory could never contain it and the guard would be
	// meaningless. The certificate is retained.
	CertificateDecisionInvalidOwner CertificateDecision = "invalid-owner"
	// CertificateDecisionLiveOwner means the owner token still matches a resource group.
	CertificateDecisionLiveOwner CertificateDecision = "live-owner"
	// CertificateDecisionInvalidReferenceTime means retention could not be evaluated without a reference time.
	CertificateDecisionInvalidReferenceTime CertificateDecision = "invalid-reference-time"
	// CertificateDecisionMissingTimestamp means created/updated timestamps are unavailable.
	CertificateDecisionMissingTimestamp CertificateDecision = "missing-timestamp"
	// CertificateDecisionFutureTimestamp means a timestamp is later than the reference time.
	CertificateDecisionFutureTimestamp CertificateDecision = "future-timestamp"
	// CertificateDecisionRecent means the certificate is within the rule's retention window.
	CertificateDecisionRecent CertificateDecision = "recent"
)

// Selected reports whether the decision selects the certificate for cleanup.
func (d CertificateDecision) Selected() bool {
	return d == CertificateDecisionSelected
}

// CertificateMetadata is the policy-relevant subset of Key Vault certificate metadata.
type CertificateMetadata struct {
	Name    string
	Created *time.Time
	Updated *time.Time
	Tags    map[string]*string
}

// CertificateSelection describes the rule and owner token selected for a certificate.
type CertificateSelection struct {
	RuleIndex  int
	RuleName   string
	OwnerToken string
}

// SelectActive evaluates active certificate metadata using first-match-wins rules.
func (p *CertificateDiscoveryPolicy) SelectActive(
	certificate CertificateMetadata,
	knownOwnerTokens sets.Set[string],
	now time.Time,
) (CertificateSelection, CertificateDecision) {
	return p.selectCertificate(certificate, knownOwnerTokens, now, true)
}

// SelectDeleted evaluates deleted certificate metadata using the same name and
// protected-tag safeguards as active discovery. Neither retention nor the owner
// guard is reapplied: the certificate is already deleted, so the owner cannot
// lose anything, and the lingering tombstone only blocks reuse of the name.
func (p *CertificateDiscoveryPolicy) SelectDeleted(certificate CertificateMetadata) (CertificateSelection, CertificateDecision) {
	return p.selectCertificate(certificate, nil, time.Time{}, false)
}

func (p *CertificateDiscoveryPolicy) selectCertificate(
	certificate CertificateMetadata,
	knownOwnerTokens sets.Set[string],
	now time.Time,
	active bool, // deleted certificates skip the owner guard and retention
) (CertificateSelection, CertificateDecision) {
	name := strings.TrimSpace(certificate.Name)
	if name == "" {
		return CertificateSelection{}, CertificateDecisionInvalidID
	}
	if p.protects(certificate.Tags) {
		return CertificateSelection{}, CertificateDecisionProtectedTag
	}

	for idx, rule := range p.Rules {
		match := rule.Match.NameRegex.FindStringSubmatch(name)
		if match == nil {
			continue
		}

		selection := CertificateSelection{RuleIndex: idx, RuleName: rule.Name}
		if rule.Match.OwnerCaptureGroup != "" {
			captureIndex := rule.Match.NameRegex.SubexpIndex(rule.Match.OwnerCaptureGroup)
			if captureIndex <= 0 || captureIndex >= len(match) {
				return selection, CertificateDecisionMissingOwner
			}
			selection.OwnerToken = strings.ToLower(strings.TrimSpace(match[captureIndex]))
			if selection.OwnerToken == "" {
				return selection, CertificateDecisionMissingOwner
			}
		}

		if rule.Action == CertificateDiscoveryActionSkip {
			return selection, CertificateDecisionSkippedByRule
		}
		if !active {
			return selection, CertificateDecisionSelected
		}
		if rule.Conditions.OwnerResourceGroupAbsent && !IsOwnerToken(selection.OwnerToken) {
			return selection, CertificateDecisionInvalidOwner
		}
		if rule.Conditions.OwnerResourceGroupAbsent && knownOwnerTokens.Has(selection.OwnerToken) {
			return selection, CertificateDecisionLiveOwner
		}
		if now.IsZero() {
			return selection, CertificateDecisionInvalidReferenceTime
		}
		if certificate.Created == nil || certificate.Updated == nil || certificate.Created.IsZero() || certificate.Updated.IsZero() {
			return selection, CertificateDecisionMissingTimestamp
		}
		if certificate.Created.After(now) || certificate.Updated.After(now) {
			return selection, CertificateDecisionFutureTimestamp
		}
		cutoff := now.Add(-rule.OlderThan)
		if !certificate.Created.Before(cutoff) || !certificate.Updated.Before(cutoff) {
			return selection, CertificateDecisionRecent
		}
		return selection, CertificateDecisionSelected
	}

	return CertificateSelection{}, CertificateDecisionNoRuleMatch
}

// protects reports whether any protected tag is present with its protected
// value. Every actual tag is checked: keys that differ only in case can coexist
// in certificate metadata, and a protective one must not be hidden by another.
func (p *CertificateDiscoveryPolicy) protects(tags map[string]*string) bool {
	for key, value := range tags {
		if value == nil {
			continue
		}
		for protectedKey, protectedValue := range p.ProtectedTags {
			if strings.EqualFold(key, protectedKey) && strings.EqualFold(strings.TrimSpace(*value), strings.TrimSpace(protectedValue)) {
				return true
			}
		}
	}
	return false
}

// OwnerTokens splits a resource group name into the lower-cased tokens the owner
// guard compares certificate owners against. Everything that is not a letter or
// digit separates tokens.
func OwnerTokens(resourceGroupName string) []string {
	return strings.FieldsFunc(strings.ToLower(resourceGroupName), isOwnerSeparator)
}

// IsOwnerToken reports whether a captured owner is a single token that
// OwnerTokens could produce, and so can be found in the owner inventory.
func IsOwnerToken(owner string) bool {
	return owner != "" && !strings.ContainsFunc(owner, isOwnerSeparator)
}

func isOwnerSeparator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
