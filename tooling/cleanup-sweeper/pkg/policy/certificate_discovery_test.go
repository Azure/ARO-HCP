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
	"regexp"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestCertificateDiscoveryFirstMatchRetentionAndOwner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 30, 8, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	pol := CertificateDiscoveryPolicy{ProtectedTags: map[string]string{"persist": "true"}, Rules: []CertificateDiscoveryRule{
		{
			Name:   "protect-shared",
			Action: CertificateDiscoveryActionSkip,
			Match:  CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^frontend-cert-dev-usw3$`)},
		},
		{
			Name:   "personal",
			Action: CertificateDiscoveryActionDelete,
			Match: CertificateDiscoveryMatch{
				NameRegex:         regexp.MustCompile(`^frontend-cert-pers-(?P<owner>[a-z0-9]+)$`),
				OwnerCaptureGroup: "owner",
			},
			Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true},
			OlderThan:  7 * 24 * time.Hour,
		},
	}}

	testCases := []struct {
		name     string
		cert     CertificateMetadata
		owners   sets.Set[string]
		selected bool
		reason   CertificateDecision
	}{
		{
			name:     "first skip rule protects shared certificate",
			cert:     CertificateMetadata{Name: "frontend-cert-dev-usw3", Created: &old, Updated: &old},
			selected: false,
			reason:   CertificateDecisionSkippedByRule,
		},
		{
			name:     "owner token vetoes personal certificate",
			cert:     CertificateMetadata{Name: "frontend-cert-pers-usw3rvaz", Created: &old, Updated: &old},
			owners:   sets.New("usw3rvaz"),
			selected: false,
			reason:   CertificateDecisionLiveOwner,
		},
		{
			name:     "orphaned old personal certificate is selected",
			cert:     CertificateMetadata{Name: "frontend-cert-pers-usw3rvaz", Created: &old, Updated: &old},
			selected: true,
			reason:   CertificateDecisionSelected,
		},
		{
			name: "protected tag wins before delete rule",
			cert: CertificateMetadata{
				Name:    "frontend-cert-pers-usw3rvaz",
				Created: &old,
				Updated: &old,
				Tags:    map[string]*string{"persist": stringPtr("true")},
			},
			selected: false,
			reason:   CertificateDecisionProtectedTag,
		},
		{
			name: "recent renewal is retained",
			cert: func() CertificateMetadata {
				recent := now.Add(-24 * time.Hour)
				return CertificateMetadata{Name: "frontend-cert-pers-usw3rvaz", Created: &old, Updated: &recent}
			}(),
			selected: false,
			reason:   CertificateDecisionRecent,
		},
		{
			name:     "unmatched certificate is retained",
			cert:     CertificateMetadata{Name: "firstPartyCert", Created: &old, Updated: &old},
			selected: false,
			reason:   CertificateDecisionNoRuleMatch,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			selection, reason := pol.SelectActive(tc.cert, tc.owners, now)
			if got := reason.Selected(); got != tc.selected {
				t.Fatalf("selected=%t, want %t (reason %q, selection %#v)", got, tc.selected, reason, selection)
			}
			if reason != tc.reason {
				t.Fatalf("reason=%q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestCertificateDiscoveryDeletedIgnoresOwnerGuardAndRetention(t *testing.T) {
	t.Parallel()

	pol := CertificateDiscoveryPolicy{ProtectedTags: map[string]string{"persist": "true"}, Rules: []CertificateDiscoveryRule{
		{
			Name:   "protect-shared",
			Action: CertificateDiscoveryActionSkip,
			Match:  CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^maestro-server-shared$`)},
		},
		{
			Name:   "personal",
			Action: CertificateDiscoveryActionDelete,
			Match: CertificateDiscoveryMatch{
				NameRegex:         regexp.MustCompile(`^maestro-server-(?P<owner>[a-z0-9]+)$`),
				OwnerCaptureGroup: "owner",
			},
			Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true},
			OlderThan:  7 * 24 * time.Hour,
		},
	}}

	// A live owner and a missing timestamp do not retain a tombstone: the name is
	// already deleted and only blocks reuse.
	if _, decision := pol.SelectDeleted(CertificateMetadata{Name: "maestro-server-usw3rvaz"}); !decision.Selected() {
		t.Fatalf("expected tombstone to be selected, got %q", decision)
	}
	// Rule matching and tag protection still apply.
	if _, decision := pol.SelectDeleted(CertificateMetadata{Name: "maestro-server-shared"}); decision != CertificateDecisionSkippedByRule {
		t.Fatalf("expected skip rule to retain tombstone, got %q", decision)
	}
	protected := map[string]*string{"persist": stringPtr("true")}
	if _, decision := pol.SelectDeleted(CertificateMetadata{Name: "maestro-server-usw3rvaz", Tags: protected}); decision != CertificateDecisionProtectedTag {
		t.Fatalf("expected protected tag to retain tombstone, got %q", decision)
	}
	if _, decision := pol.SelectDeleted(CertificateMetadata{Name: "unrelated"}); decision != CertificateDecisionNoRuleMatch {
		t.Fatalf("expected unmatched tombstone to be retained, got %q", decision)
	}
}

func stringPtr(value string) *string {
	return &value
}
