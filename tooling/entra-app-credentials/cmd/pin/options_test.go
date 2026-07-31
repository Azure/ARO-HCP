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

package pin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		options   RawOptions
		wantApps  []string
		wantError string
	}{
		{
			name: "explicit and indexed mappings",
			options: RawOptions{
				VaultURL:               "https://example.vault.azure.net",
				Mappings:               []string{"app-a=cert-a", "app-b = cert-b"},
				IndexedApplicationBase: "pool-app",
				IndexedCertificateBase: "pool-cert",
				IndexedCount:           2,
				ReplaceAll:             true,
				Timeout:                time.Minute,
			},
			wantApps: []string{"app-a", "app-b", "pool-app-0", "pool-app-1"},
		},
		{
			name: "replace all required",
			options: RawOptions{
				VaultURL: "https://example.vault.azure.net",
				Mappings: []string{"app=cert"},
				Timeout:  time.Minute,
			},
			wantError: "--replace-all is required",
		},
		{
			name: "duplicate application rejected",
			options: RawOptions{
				VaultURL:   "https://example.vault.azure.net",
				Mappings:   []string{"app=cert-a", "app=cert-b"},
				ReplaceAll: true,
				Timeout:    time.Minute,
			},
			wantError: "specified more than once",
		},
		{
			name: "indexed bases required",
			options: RawOptions{
				VaultURL:     "https://example.vault.azure.net",
				IndexedCount: 1,
				ReplaceAll:   true,
				Timeout:      time.Minute,
			},
			wantError: "--indexed-application-base",
		},
		{
			name: "vault path rejected",
			options: RawOptions{
				VaultURL:   "https://example.vault.azure.net/certificates",
				Mappings:   []string{"app=cert"},
				ReplaceAll: true,
				Timeout:    time.Minute,
			},
			wantError: "--vault-url",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validated, err := test.options.Validate()
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			apps := make([]string, 0, len(validated.bindings))
			for _, binding := range validated.bindings {
				apps = append(apps, binding.ApplicationDisplayName)
			}
			assert.Equal(t, test.wantApps, apps)
		})
	}
}

func TestParseMapping(t *testing.T) {
	binding, err := parseMapping(" app = cert ")
	require.NoError(t, err)
	assert.Equal(t, "app", binding.ApplicationDisplayName)
	assert.Equal(t, "cert", binding.CertificateName)

	_, err = parseMapping("invalid")
	require.ErrorContains(t, err, "expected app=certificate")
}
