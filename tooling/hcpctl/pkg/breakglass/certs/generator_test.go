// Copyright 2025 Microsoft Corporation
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

package certs

import (
	"crypto/x509/pkix"
	"testing"
)

func TestGenerateCSR(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	subject := BuildSubject("testuser", false)
	csrPEM, err := GenerateCSR(key, subject)
	if err != nil {
		t.Fatalf("failed to generate CSR: %v", err)
	}

	if len(csrPEM) == 0 {
		t.Fatal("CSR PEM should not be empty")
	}
}

func TestBuildSubject(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		privileged bool
		expected   pkix.Name
	}{
		{
			name:       "unprivileged-session",
			user:       "b-user@microsoft.com",
			privileged: false,
			expected: pkix.Name{
				CommonName:   "system:sre-break-glass:b-user@microsoft.com",
				Organization: []string{"aro-sre"},
			},
		},
		{
			name:       "privileged-session",
			user:       "b-user@microsoft.com",
			privileged: true,
			expected: pkix.Name{
				CommonName:   "system:sre-break-glass:b-user@microsoft.com",
				Organization: []string{"aro-sre-cluster-admin"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject := BuildSubject(tc.user, tc.privileged)

			if subject.CommonName != tc.expected.CommonName {
				t.Errorf("expected CN %s, got %s", tc.expected.CommonName, subject.CommonName)
			}

			if len(subject.Organization) != len(tc.expected.Organization) {
				t.Errorf("expected Organization length %d, got %d", len(tc.expected.Organization), len(subject.Organization))
			}

			for i, expectedOrg := range tc.expected.Organization {
				if i >= len(subject.Organization) || subject.Organization[i] != expectedOrg {
					t.Errorf("expected Organization %v, got %v", tc.expected.Organization, subject.Organization)
					break
				}
			}
		})
	}
}
