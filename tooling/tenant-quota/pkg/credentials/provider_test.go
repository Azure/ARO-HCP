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

package credentials

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

func testPair(t *testing.T) (string, config.TenantConfig) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(secretsStoreEnvVar, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "client-id"), []byte("11111111-1111-1111-1111-111111111111\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "client-secret"), []byte("test-password\n"), 0600))
	return root, config.TenantConfig{TenantID: "22222222-2222-2222-2222-222222222222", ClientIDKeyVaultSecretName: "client-id", ClientSecretKeyVaultSecretName: "client-secret"}
}

func TestStartupValidation(t *testing.T) {
	for _, field := range []string{"client-id", "client-secret"} {
		for _, problem := range []string{"missing", "empty", "whitespace", "directory", "malformed"} {
			t.Run(field+"/"+problem, func(t *testing.T) {
				root, tenant := testPair(t)
				path := filepath.Join(root, field)
				require.NoError(t, os.Remove(path))
				switch problem {
				case "empty":
					require.NoError(t, os.WriteFile(path, nil, 0600))
				case "whitespace":
					require.NoError(t, os.WriteFile(path, []byte(" \n\t"), 0600))
				case "directory":
					require.NoError(t, os.Mkdir(path, 0700))
				case "malformed":
					if field == "client-secret" {
						t.Skip("nonempty passwords have no syntax constraint")
					}
					require.NoError(t, os.WriteFile(path, []byte("not-a-uuid-sensitive"), 0600))
				}
				err := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil))).ValidateCredentials([]config.TenantConfig{tenant})
				require.Error(t, err)
				require.NotContains(t, err.Error(), "test-password")
				require.NotContains(t, err.Error(), "not-a-uuid-sensitive")
			})
		}
	}
}

func TestCredentialDiagnosticsExcludeReferencesAndValues(t *testing.T) {
	for _, field := range []string{"client-id", "client-secret"} {
		for _, problem := range []string{"missing", "empty", "whitespace", "directory", "malformed", "invalid-reference", "missing-reference", "shared-reference"} {
			t.Run(field+"/"+problem, func(t *testing.T) {
				if field == "client-secret" && problem == "malformed" {
					t.Skip("nonempty passwords have no syntax constraint")
				}
				root, tenant := testPair(t)
				tenant.TenantName = "diagnostic-tenant"
				tenant.ClientIDKeyVaultSecretName = "sentinel-id-reference"
				tenant.ClientSecretKeyVaultSecretName = "sentinel-password-reference"
				require.NoError(t, os.Rename(filepath.Join(root, "client-id"), filepath.Join(root, tenant.ClientIDKeyVaultSecretName)))
				require.NoError(t, os.Rename(filepath.Join(root, "client-secret"), filepath.Join(root, tenant.ClientSecretKeyVaultSecretName)))
				reference := &tenant.ClientIDKeyVaultSecretName
				category := "client ID"
				referenceField := "clientIdKeyVaultSecretName"
				if field == "client-secret" {
					reference = &tenant.ClientSecretKeyVaultSecretName
					category = "client secret"
					referenceField = "clientSecretKeyVaultSecretName"
				}
				path := filepath.Join(root, *reference)
				switch problem {
				case "missing":
					require.NoError(t, os.Remove(path))
					category += ": cannot read credential file"
				case "empty", "whitespace":
					content := ""
					if problem == "whitespace" {
						content = " \n\t"
					}
					require.NoError(t, os.WriteFile(path, []byte(content), 0600))
					category += ": credential file is empty"
				case "directory":
					require.NoError(t, os.Remove(path))
					require.NoError(t, os.Mkdir(path, 0700))
					category += ": cannot read credential file"
				case "malformed":
					require.NoError(t, os.WriteFile(path, []byte("sentinel-invalid-client-id-content"), 0600))
					category += " file must contain a UUID"
				case "invalid-reference", "missing-reference":
					*reference = "../" + *reference
					category = referenceField + " must be a Key Vault secret name"
					if problem == "missing-reference" {
						*reference = ""
						category = referenceField + " is required"
					}
				case "shared-reference":
					tenant.ClientSecretKeyVaultSecretName = tenant.ClientIDKeyVaultSecretName
					category = "client ID and client secret references must differ"
				}
				var output bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&output, nil))
				err := NewProvider(logger).ValidateCredentials([]config.TenantConfig{tenant})
				require.Error(t, err)
				logger.Error("Credential validation failed", "error", err)
				for _, diagnostic := range []string{err.Error(), output.String()} {
					for _, sensitive := range []string{"sentinel-id-reference", "sentinel-password-reference", "test-password", "11111111-1111-1111-1111-111111111111", "sentinel-invalid-client-id-content", root} {
						require.NotContains(t, diagnostic, sensitive)
					}
					require.Contains(t, diagnostic, "diagnostic-tenant")
					require.Contains(t, diagnostic, category)
				}
			})
		}
	}
}

func TestRotationRequiresRestart(t *testing.T) {
	for _, field := range []string{"client-id", "client-secret"} {
		t.Run(field, func(t *testing.T) {
			root, tenant := testPair(t)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			running := NewProvider(logger)
			original, err := running.GetCredential(tenant)
			require.NoError(t, err)
			replacement := "replacement-password"
			if field == "client-id" {
				replacement = "33333333-3333-3333-3333-333333333333"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, field), []byte(replacement), 0600))
			cached, err := running.GetCredential(tenant)
			require.NoError(t, err)
			require.Same(t, original, cached)
			restarted, err := NewProvider(logger).GetCredential(tenant)
			require.NoError(t, err)
			require.NotSame(t, original, restarted)
			require.NoError(t, os.Remove(filepath.Join(root, field)))
			cached, err = running.GetCredential(tenant)
			require.NoError(t, err)
			require.Same(t, original, cached)
			require.Error(t, NewProvider(logger).ValidateCredentials([]config.TenantConfig{tenant}))
		})
	}
}

func TestCredentialReferencesAndIsolation(t *testing.T) {
	root, tenant := testPair(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := NewProvider(logger)
	first, err := provider.GetCredential(tenant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "other-id"), []byte("44444444-4444-4444-4444-444444444444"), 0600))
	tenant.ClientIDKeyVaultSecretName = "other-id"
	second, err := provider.GetCredential(tenant)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	for _, name := range []string{"", "../client-id", "/client-id", "bad_name", "client-secret", strings.Repeat("a", 128)} {
		tenant.ClientIDKeyVaultSecretName = name
		_, err := provider.GetCredential(tenant)
		require.Error(t, err)
	}
}

func TestCSIGenerationSnapshot(t *testing.T) {
	root, tenant := testPair(t)
	generation := filepath.Join(root, "generation")
	require.NoError(t, os.Mkdir(generation, 0700))
	for _, name := range []string{"client-id", "client-secret"} {
		require.NoError(t, os.Rename(filepath.Join(root, name), filepath.Join(generation, name)))
		require.NoError(t, os.Symlink(filepath.Join("..data", name), filepath.Join(root, name)))
	}
	require.NoError(t, os.Symlink("generation", filepath.Join(root, "..data")))
	provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, provider.ValidateCredentials([]config.TenantConfig{tenant}))
	require.NoError(t, os.Remove(filepath.Join(root, "..data")))
	require.NoError(t, os.Symlink("missing-generation", filepath.Join(root, "..data")))
	require.Error(t, NewProvider(provider.logger).ValidateCredentials([]config.TenantConfig{tenant}))
}

func TestConcurrentCredentialCacheAndTenantIsolation(t *testing.T) {
	_, tenant := testPair(t)
	provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	first, err := provider.GetCredential(tenant)
	require.NoError(t, err)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			credential, err := provider.GetCredential(tenant)
			if err != nil {
				t.Errorf("GetCredential failed: %v", err)
				return
			}
			if credential != first {
				t.Error("concurrent callers must share the startup SDK credential")
			}
		})
	}
	workers.Wait()
	otherTenant := tenant
	otherTenant.TenantID = "55555555-5555-5555-5555-555555555555"
	second, err := provider.GetCredential(otherTenant)
	require.NoError(t, err)
	require.NotSame(t, first, second, "shared references must not share SDK credentials across tenants")
}
