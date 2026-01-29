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

package utils

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// atomicUpdateFile simulates a configmap/secret/secretproviderclass rotation using the AtomicWriter pattern.
// Kubernetes CSI SecretProviderClass and Secrets/ConfigMaps use this pattern:
// secrets are written into a new timestamped dir, a ..data_tmp symlink is created pointing to it,
// and then rename(..data_tmp, ..data) atomically replaces the old ..data symlink.
// Even though we don't use fsnotify yet, this pattern is still useful for testing.
func atomicUpdateFile(t *testing.T, dir, filename, version, content string) {
	t.Helper()

	versionedDir := filepath.Join(dir, fmt.Sprintf("..%s", version))
	err := os.MkdirAll(versionedDir, 0755)
	require.NoError(t, err)

	secretPath := filepath.Join(versionedDir, filename)
	err = os.WriteFile(secretPath, []byte(content), 0644)
	require.NoError(t, err)

	dataLink := filepath.Join(dir, "..data")
	dataTmpLink := filepath.Join(dir, "..data_tmp")

	_ = os.Remove(dataTmpLink)

	err = os.Symlink(filepath.Base(versionedDir), dataTmpLink)
	require.NoError(t, err)

	err = os.Rename(dataTmpLink, dataLink)
	require.NoError(t, err)

	secretLink := filepath.Join(dir, filename)
	if _, err := os.Lstat(secretLink); os.IsNotExist(err) {
		err = os.Symlink(filepath.Join("..data", filename), secretLink)
		require.NoError(t, err)
	}
}

type fileUpdateReceiver struct {
	lastContent string
	filePath    string
}

func (r *fileUpdateReceiver) onUpdate(_ context.Context) error {
	content, err := os.ReadFile(r.filePath)
	if err != nil {
		return err
	}
	r.lastContent = string(content)
	return nil
}

func TestFSWatcherDetectsRotation(t *testing.T) {
	mountDir := t.TempDir()
	secretFileName := "secret.txt"
	secretPath := filepath.Join(mountDir, secretFileName)

	atomicUpdateFile(t, mountDir, secretFileName, "v1", "initial-value")
	receiver := &fileUpdateReceiver{
		filePath: secretPath,
	}

	watcher, err := NewFSWatcher(
		secretPath,
		50*time.Millisecond,
		receiver.onUpdate,
	)
	require.NoError(t, err)
	ctx := ContextWithLogger(t.Context(), testr.New(t))
	err = watcher.Start(ctx)
	require.NoError(t, err)

	rotatedSecretValue := "rotated-secret"
	atomicUpdateFile(t, mountDir, secretFileName, "v2", rotatedSecretValue)
	assert.Eventually(t, func() bool {
		return receiver.lastContent == rotatedSecretValue
	}, 2*time.Second, 50*time.Millisecond, "callback should be invoked after rotation and return the new value")
}

func TestFSWatcherContextCancellation(t *testing.T) {
	mountDir := t.TempDir()
	secretFileName := "secret.txt"

	atomicUpdateFile(t, mountDir, secretFileName, "v1", "initial")
	secretPath := filepath.Join(mountDir, secretFileName)

	receiver := &fileUpdateReceiver{
		filePath: secretPath,
	}
	ctx, cancel := context.WithCancel(t.Context())
	ctx = ContextWithLogger(ctx, testr.New(t))
	watcher, err := NewFSWatcher(secretPath, 50*time.Millisecond, receiver.onUpdate)
	require.NoError(t, err)

	err = watcher.Start(ctx)
	require.NoError(t, err)

	// Update file before cancellation - should be detected
	atomicUpdateFile(t, mountDir, secretFileName, "v2", "before-cancel")
	assert.Eventually(t, func() bool {
		return receiver.lastContent == "before-cancel"
	}, 2*time.Second, 50*time.Millisecond, "callback should be invoked after rotation and return the new value")

	// Cancel the watcher
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Update file after cancellation - should NOT be detected
	atomicUpdateFile(t, mountDir, secretFileName, "v3", "after-cancel")
	assert.Never(t, func() bool {
		return receiver.lastContent == "after-cancel"
	}, 2*time.Second, 50*time.Millisecond, "callback should not be invoked after cancellation")
}

func TestFSWatcherRequiresCallback(t *testing.T) {
	_, err := NewFSWatcher("/some/path", 100*time.Millisecond, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "onChange callback is required")
}

func TestFSWatcherInvalidPath(t *testing.T) {
	onChange := func(_ context.Context) error {
		return nil
	}

	ctx := ContextWithLogger(context.Background(), testr.New(t))

	// Try to watch a path that doesn't exist
	watcher, err := NewFSWatcher("/nonexistent/path/secret", 100*time.Millisecond, onChange)
	require.NoError(t, err)

	// Start should fail because the file doesn't exist
	err = watcher.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to hash file")
}
