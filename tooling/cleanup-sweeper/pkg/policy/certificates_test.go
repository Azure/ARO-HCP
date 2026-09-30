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
  discovery:
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

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
