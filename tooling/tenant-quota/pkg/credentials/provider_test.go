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
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

func TestCredentialsAreCachedUntilRestart(t *testing.T) {
	root, tenant := testPair(t)
	provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	first, err := provider.GetCredential(tenant)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(root, "client-secret"), []byte("rotated-password\n"), 0600))
	second, err := provider.GetCredential(tenant)
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestWatchingTriggersOnChangeForAnyCredentialFile(t *testing.T) {
	for _, file := range []string{"client-id", "client-secret"} {
		t.Run(file, func(t *testing.T) {
			root, tenant := testPair(t)
			provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
			provider.watchInterval = 10 * time.Millisecond
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, provider.StartWatching(ctx, []config.TenantConfig{tenant}, func() { calls.Add(1) }))

			time.Sleep(50 * time.Millisecond)
			require.Zero(t, calls.Load(), "unchanged files must not trigger shutdown")
			require.NoError(t, os.WriteFile(filepath.Join(root, file), []byte("changed\n"), 0600))
			require.Eventually(t, func() bool { return calls.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
		})
	}
}

// A rotation that lands after the watcher baseline but before the credential is
// loaded must still be detected, so the process cannot keep stale credentials.
func TestWatchingDetectsRotationBeforeCredentialsAreLoaded(t *testing.T) {
	root, tenant := testPair(t)
	provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.watchInterval = 10 * time.Millisecond
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, provider.StartWatching(ctx, []config.TenantConfig{tenant}, func() { calls.Add(1) }))

	require.NoError(t, os.WriteFile(filepath.Join(root, "client-secret"), []byte("rotated\n"), 0600))
	_, err := provider.GetCredential(tenant)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return calls.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
}

// createGeneration writes one CSI-style key rotation generation and atomically
// swaps the ..data symlink to it, as the secrets-store driver does.
func createGeneration(t *testing.T, root, generation string, files map[string]string) {
	t.Helper()
	directory := filepath.Join(root, generation)
	require.NoError(t, os.Mkdir(directory, 0700))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(content), 0600))
		if _, err := os.Lstat(filepath.Join(root, name)); os.IsNotExist(err) {
			require.NoError(t, os.Symlink(filepath.Join("..data", name), filepath.Join(root, name)))
		}
	}
	next := filepath.Join(root, "..data_tmp")
	require.NoError(t, os.Symlink(generation, next))
	require.NoError(t, os.Rename(next, filepath.Join(root, "..data")))
}

func TestWatchingFollowsCSIGenerationRotations(t *testing.T) {
	root := t.TempDir()
	t.Setenv(secretsStoreEnvVar, root)
	clientID := "11111111-1111-1111-1111-111111111111"
	createGeneration(t, root, "..2026_11_24_17_00_00.000000000", map[string]string{
		"client-id":     clientID + "\n",
		"client-secret": "test-password\n",
	})
	tenant := config.TenantConfig{TenantID: "22222222-2222-2222-2222-222222222222", ClientIDKeyVaultSecretName: "client-id", ClientSecretKeyVaultSecretName: "client-secret"}

	provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.watchInterval = 10 * time.Millisecond
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, provider.StartWatching(ctx, []config.TenantConfig{tenant}, func() { calls.Add(1) }))
	_, err := provider.GetCredential(tenant)
	require.NoError(t, err)

	// Rotate both objects in a new generation. The client ID is no longer a
	// UUID so a provider that resolves the new generation must reject it.
	createGeneration(t, root, "..2026_11_24_17_30_00.000000000", map[string]string{
		"client-id":     "rotated-client-id\n",
		"client-secret": "rotated-password\n",
	})

	require.Eventually(t, func() bool { return calls.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, int32(1), calls.Load(), "the shutdown callback must fire once")

	_, err = NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil))).GetCredential(tenant)
	require.ErrorContains(t, err, "client ID file must contain a UUID")
}
