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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLoadCertificates_ParsesRegexDurationAndOwnerCapture(t *testing.T) {
	t.Parallel()

	loaded, err := LoadCertificates(writeTemp(t, `
certificates:
  vaultURL: https://example.vault.azure.net/
  ownerInventory:
    subscriptions: [Example Subscription]
  discovery:
    protectedTags: {Persist: " true "}
    rules:
    - name: personal
      action: delete
      match:
        nameRegex: "^frontend-cert-pers-(?P<owner>[a-z0-9]+)$"
        ownerCaptureGroup: owner
      conditions:
        ownerResourceGroupAbsent: true
      olderThan: 168h
`))
	if err != nil {
		t.Fatalf("expected policy to load: %v", err)
	}
	if loaded.VaultURL != "https://example.vault.azure.net" {
		t.Fatalf("vaultURL=%q, want normalized host URL", loaded.VaultURL)
	}
	if got := loaded.Discovery.ProtectedTags; len(got) != 1 || got["persist"] != "true" {
		t.Fatalf("protectedTags=%v, want normalized persist=true", got)
	}
	rule := loaded.Discovery.Rules[0]
	if rule.OlderThan != 7*24*time.Hour {
		t.Fatalf("olderThan=%s, want 168h", rule.OlderThan)
	}
	if got := rule.Match.NameRegex.SubexpIndex("owner"); got < 1 {
		t.Fatalf("owner capture index=%d, want positive", got)
	}
}

func TestLoadCertificates_RejectsUnknownKeys(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"misspelled guard condition": `
certificates:
  discovery:
    rules:
    - {name: r, action: delete, olderThan: 168h, match: {nameRegex: "^x-(?P<o>a)$", ownerCaptureGroup: o}, conditions: {ownerResourceGroupAbsnt: true}}
`,
		"misspelled rule field": `
certificates:
  discovery:
    rules:
    - {name: r, action: skip, match: {nameRegex: "^x$"}, olderThen: 1h}
`,
		"misspelled match field": `
certificates:
  discovery:
    rules:
    - {name: r, action: skip, match: {nameRegx: "^x$"}}
`,
		"unrelated top-level section": `
rgOrdered: {}
certificates:
  discovery:
    rules:
    - {name: r, action: skip, match: {nameRegex: "^x$"}}
`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadCertificates(writeTemp(t, doc)); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("error = %v, want unknown field", err)
			}
		})
	}
}

func TestCertificateRuleValidate_OwnerGuardIsMandatoryWithExplicitOptOut(t *testing.T) {
	t.Parallel()

	withCapture := CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x-(?P<owner>[a-z0-9]+)$`), OwnerCaptureGroup: "owner"}
	withoutCapture := CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x-[a-z0-9]+$`)}

	testCases := []struct {
		name    string
		rule    CertificateDiscoveryRule
		wantErr string
	}{
		{
			name: "guarded delete",
			rule: CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withCapture, Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true}, OlderThan: 24 * time.Hour},
		},
		{
			name: "explicit opt-out",
			rule: CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withoutCapture, AllowWithoutOwnerGuard: true, OlderThan: 24 * time.Hour},
		},
		{
			name:    "delete without guard or opt-out",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withoutCapture, OlderThan: 24 * time.Hour},
			wantErr: "delete rules must set conditions.ownerResourceGroupAbsent",
		},
		{
			name:    "capture group does not substitute for the guard",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withCapture, OlderThan: 24 * time.Hour},
			wantErr: "delete rules must set conditions.ownerResourceGroupAbsent",
		},
		{
			name:    "guard and opt-out together",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withCapture, Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true}, AllowWithoutOwnerGuard: true, OlderThan: 24 * time.Hour},
			wantErr: "cannot be combined",
		},
		{
			name:    "opt-out with unused capture",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withCapture, AllowWithoutOwnerGuard: true, OlderThan: 24 * time.Hour},
			wantErr: "must be omitted when allowWithoutOwnerGuard",
		},
		{
			name:    "opt-out on skip rule",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionSkip, Match: withoutCapture, AllowWithoutOwnerGuard: true},
			wantErr: "must be omitted for skip action",
		},
		{
			name:    "retention below minimum",
			rule:    CertificateDiscoveryRule{Name: "r", Action: CertificateDiscoveryActionDelete, Match: withCapture, Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true}, OlderThan: 23 * time.Hour},
			wantErr: "at least 24h",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.rule.Validate("rule")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestCertificatePolicyValidate_ScopeAndProtectedTags(t *testing.T) {
	t.Parallel()

	guarded := CertificateDiscoveryRule{
		Name:       "r",
		Action:     CertificateDiscoveryActionDelete,
		Match:      CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x-(?P<owner>[a-z0-9]+)$`), OwnerCaptureGroup: "owner"},
		Conditions: CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true},
		OlderThan:  24 * time.Hour,
	}
	unguarded := CertificateDiscoveryRule{
		Name:                   "r",
		Action:                 CertificateDiscoveryActionDelete,
		Match:                  CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x$`)},
		AllowWithoutOwnerGuard: true,
		OlderThan:              24 * time.Hour,
	}
	valid := func(mutate func(*CertificatePolicy)) CertificatePolicy {
		pol := CertificatePolicy{
			VaultURL:       "https://example.vault.azure.net",
			OwnerInventory: CertificateOwnerInventory{Subscriptions: []string{"Sub A", "Sub B"}},
			Discovery: CertificateDiscoveryPolicy{
				ProtectedTags: map[string]string{"persist": "true"},
				Rules:         []CertificateDiscoveryRule{guarded},
			},
		}
		if mutate != nil {
			mutate(&pol)
		}
		return pol
	}

	testCases := []struct {
		name    string
		mutate  func(*CertificatePolicy)
		wantErr string
	}{
		{name: "valid"},
		{name: "no subscriptions needed without guarded rules", mutate: func(p *CertificatePolicy) {
			p.OwnerInventory = CertificateOwnerInventory{}
			p.Discovery.Rules = []CertificateDiscoveryRule{unguarded}
		}},
		{name: "missing vault", mutate: func(p *CertificatePolicy) { p.VaultURL = "" }, wantErr: "vaultURL: is required"},
		{name: "http vault", mutate: func(p *CertificatePolicy) { p.VaultURL = "http://example.vault.azure.net" }, wantErr: "https URL"},
		{name: "vault with path", mutate: func(p *CertificatePolicy) { p.VaultURL = "https://example.vault.azure.net/certificates" }, wantErr: "https URL"},
		{name: "vault name instead of URL", mutate: func(p *CertificatePolicy) { p.VaultURL = "example" }, wantErr: "https URL"},
		{name: "missing protected tags", mutate: func(p *CertificatePolicy) { p.Discovery.ProtectedTags = nil }, wantErr: "protectedTags: must define at least one"},
		{name: "empty protected tag key", mutate: func(p *CertificatePolicy) { p.Discovery.ProtectedTags = map[string]string{" ": "true"} }, wantErr: "cannot contain empty keys"},
		{name: "empty protected tag value", mutate: func(p *CertificatePolicy) { p.Discovery.ProtectedTags = map[string]string{"persist": " "} }, wantErr: "value is required"},
		{name: "guarded rule without subscriptions", mutate: func(p *CertificatePolicy) { p.OwnerInventory = CertificateOwnerInventory{} }, wantErr: "must name at least one subscription"},
		{name: "empty subscription name", mutate: func(p *CertificatePolicy) { p.OwnerInventory.Subscriptions = []string{"Sub A", " "} }, wantErr: "cannot be empty"},
		{name: "duplicate subscription", mutate: func(p *CertificatePolicy) { p.OwnerInventory.Subscriptions = []string{"Sub A", "Sub A"} }, wantErr: "duplicate subscription"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pol := valid(tc.mutate)
			err := pol.Validate("certificates")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestCertificateDiscovery_ProtectedTagsAreConfigurable(t *testing.T) {
	t.Parallel()

	old := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := old.Add(30 * 24 * time.Hour)
	pol := CertificateDiscoveryPolicy{
		ProtectedTags: map[string]string{"keep": "yes"},
		Rules: []CertificateDiscoveryRule{{
			Name:                   "all",
			Action:                 CertificateDiscoveryActionDelete,
			Match:                  CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^.*$`)},
			AllowWithoutOwnerGuard: true,
			OlderThan:              24 * time.Hour,
		}},
	}
	tagged := func(key, value string) CertificateMetadata {
		return CertificateMetadata{Name: "c", Created: &old, Updated: &old, Tags: map[string]*string{key: &value}}
	}
	if _, decision := pol.SelectActive(tagged("KEEP", " YES "), nil, now); decision != CertificateDecisionProtectedTag {
		t.Fatalf("configured tag decision = %q, want protected-tag", decision)
	}
	// Only configured tags protect: persist is not special-cased in code.
	if _, decision := pol.SelectActive(tagged("persist", "true"), nil, now); !decision.Selected() {
		t.Fatalf("unconfigured persist tag decision = %q, want selected", decision)
	}
	if _, decision := pol.SelectActive(tagged("keep", "no"), nil, now); !decision.Selected() {
		t.Fatalf("different value decision = %q, want selected", decision)
	}
	if _, decision := pol.SelectDeleted(tagged("keep", "yes")); decision != CertificateDecisionProtectedTag {
		t.Fatalf("deleted certificate decision = %q, want protected-tag", decision)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
