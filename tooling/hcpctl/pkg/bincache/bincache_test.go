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

package bincache

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetName(t *testing.T) {
	spec := BinarySpec{
		Name:         "must-gather-clean",
		AssetPattern: "{name}-{os}-{arch}.tar.gz",
	}

	result := assetName(spec)
	expected := "must-gather-clean-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	assert.Equal(t, expected, result)
}

func TestResolveWithExplicitPath(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		expectError bool
	}{
		{
			name: "explicit path exists",
			setup: func(t *testing.T) string {
				tmpFile := filepath.Join(t.TempDir(), "must-gather-clean")
				require.NoError(t, os.WriteFile(tmpFile, []byte("binary"), 0755))
				return tmpFile
			},
			expectError: false,
		},
		{
			name: "explicit path does not exist",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			result, err := Resolve(context.Background(), MustGatherClean, path)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, path, result)
			}
		})
	}
}

func TestResolveExplicitPathLogsMessage(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "must-gather-clean")
	require.NoError(t, os.WriteFile(tmpFile, []byte("binary"), 0755))

	var logMessages []string
	logger := funcr.New(func(prefix, args string) {
		logMessages = append(logMessages, args)
	}, funcr.Options{Verbosity: 1})

	ctx := logr.NewContext(context.Background(), logger)
	result, err := Resolve(ctx, MustGatherClean, tmpFile)

	require.NoError(t, err)
	assert.Equal(t, tmpFile, result)

	found := false
	for _, msg := range logMessages {
		if strings.Contains(msg, "using explicit binary path") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected log message about explicit path, got: %v", logMessages)
}

func TestResolveAutoDownload(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	binaryContent := []byte("#!/bin/sh\necho hello\n")
	tarGzData := createTestTarGz(t, "test-binary", binaryContent)

	spec := BinarySpec{
		Name:         "test-binary",
		Owner:        "test-owner",
		Repo:         "test-repo",
		AssetPattern: "{name}-{os}-{arch}.tar.gz",
	}

	asset := assetName(spec)
	downloadPath := fmt.Sprintf("/test-owner/test-repo/releases/download/v1.0.0/%s", asset)

	// Set up mock servers
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-owner/test-repo/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == downloadPath {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(tarGzData)
			return
		}
		http.NotFound(w, r)
	}))
	defer downloadServer.Close()

	// Override package-level URLs for test
	origAPI := githubAPIBaseURL
	origDownload := githubDownloadBaseURL
	githubAPIBaseURL = apiServer.URL
	githubDownloadBaseURL = downloadServer.URL
	t.Cleanup(func() {
		githubAPIBaseURL = origAPI
		githubDownloadBaseURL = origDownload
	})

	// Resolve without explicit path — should auto-download
	result, err := Resolve(context.Background(), spec, "")
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// Verify the binary was written
	content, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, content)

	// Resolve again — should use cached binary (no second download)
	result2, err := Resolve(context.Background(), spec, "")
	require.NoError(t, err)
	assert.Equal(t, result, result2)
}

func TestResolveOfflineFallback(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	spec := BinarySpec{
		Name:         "test-binary-offline",
		Owner:        "test-owner",
		Repo:         "test-repo",
		AssetPattern: "{name}-{os}-{arch}.tar.gz",
	}

	// Pre-populate cache with a binary
	cacheDir, err := cacheBaseDir(spec)
	require.NoError(t, err)
	versionDir := filepath.Join(cacheDir, "v0.0.1")
	require.NoError(t, os.MkdirAll(versionDir, 0755))
	cachedBin := filepath.Join(versionDir, "test-binary-offline")
	require.NoError(t, os.WriteFile(cachedBin, []byte("cached-binary"), 0755))

	// Point API at a server that always fails
	origAPI := githubAPIBaseURL
	githubAPIBaseURL = "http://localhost:1" // unreachable
	t.Cleanup(func() { githubAPIBaseURL = origAPI })

	var logMessages []string
	logger := funcr.New(func(prefix, args string) {
		logMessages = append(logMessages, args)
	}, funcr.Options{Verbosity: 4})
	ctx := logr.NewContext(context.Background(), logger)

	result, err := Resolve(ctx, spec, "")
	require.NoError(t, err)
	assert.Equal(t, cachedBin, result)

	// Verify warning was logged
	found := false
	for _, msg := range logMessages {
		if strings.Contains(msg, "cached binary") && strings.Contains(msg, "outdated") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected warning about cached binary, got: %v", logMessages)
}

func TestGitHubTokenAuth(t *testing.T) {
	var receivedAuth string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
	}))
	defer apiServer.Close()

	origAPI := githubAPIBaseURL
	githubAPIBaseURL = apiServer.URL
	t.Cleanup(func() { githubAPIBaseURL = origAPI })

	spec := BinarySpec{Owner: "test", Repo: "test"}

	t.Run("with GITHUB_TOKEN set", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "test-token-123")
		receivedAuth = ""

		version, err := getLatestVersion(context.Background(), spec)
		require.NoError(t, err)
		assert.Equal(t, "v1.0.0", version)
		assert.Equal(t, "Bearer test-token-123", receivedAuth)
	})

	t.Run("without GITHUB_TOKEN", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		receivedAuth = ""

		version, err := getLatestVersion(context.Background(), spec)
		require.NoError(t, err)
		assert.Equal(t, "v1.0.0", version)
		assert.Empty(t, receivedAuth)
	})
}

func TestCleanOldVersions(t *testing.T) {
	tmpDir := t.TempDir()

	origRoot := cacheRootDir
	cacheRootDir = tmpDir
	t.Cleanup(func() { cacheRootDir = origRoot })

	spec := BinarySpec{Name: "test-binary"}

	// Create version directories via the real cache path
	baseDir, err := cacheBaseDir(spec)
	require.NoError(t, err)

	for _, v := range []string{"v0.0.1", "v0.0.2", "v0.0.3"} {
		require.NoError(t, os.MkdirAll(filepath.Join(baseDir, v), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, v, "test-binary"), []byte("bin"), 0755))
	}

	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	// Call the actual cleanOldVersions function
	require.NoError(t, cleanOldVersions(spec, "v0.0.3"))

	entries, err = os.ReadDir(baseDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "v0.0.3", entries[0].Name())
}

func TestChecksumVerification(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho hello\n")
	tarGzData := createTestTarGz(t, "test-binary", binaryContent)

	expectedHash := fmt.Sprintf("%x", sha256.Sum256(binaryContent))

	spec := BinarySpec{
		Name:          "test-binary",
		Owner:         "test-owner",
		Repo:          "test-repo",
		AssetPattern:  "{name}-{os}-{arch}.tar.gz",
		ChecksumAsset: "SHA256_SUM",
	}

	binaryName := binaryAssetName(spec)
	asset := assetName(spec)
	downloadPath := fmt.Sprintf("/test-owner/test-repo/releases/download/v1.0.0/%s", asset)
	checksumPath := "/test-owner/test-repo/releases/download/v1.0.0/SHA256_SUM"

	setupServers := func(_ *testing.T, checksumHandler func(w http.ResponseWriter, r *http.Request)) (cleanup func()) {
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/test-owner/test-repo/releases/latest" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
				return
			}
			http.NotFound(w, r)
		}))

		downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case downloadPath:
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Write(tarGzData)
			case checksumPath:
				if checksumHandler != nil {
					checksumHandler(w, r)
				} else {
					http.NotFound(w, r)
				}
			default:
				http.NotFound(w, r)
			}
		}))

		origAPI := githubAPIBaseURL
		origDownload := githubDownloadBaseURL
		githubAPIBaseURL = apiServer.URL
		githubDownloadBaseURL = downloadServer.URL

		return func() {
			githubAPIBaseURL = origAPI
			githubDownloadBaseURL = origDownload
			apiServer.Close()
			downloadServer.Close()
		}
	}

	t.Run("valid checksum", func(t *testing.T) {
		origRoot := cacheRootDir
		cacheRootDir = t.TempDir()
		t.Cleanup(func() { cacheRootDir = origRoot })

		checksumContent := fmt.Sprintf("%s %s\n", expectedHash, binaryName)
		cleanup := setupServers(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, checksumContent)
		})
		t.Cleanup(cleanup)

		result, err := Resolve(context.Background(), spec, "")
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		content, err := os.ReadFile(result)
		require.NoError(t, err)
		assert.Equal(t, binaryContent, content)
	})

	t.Run("invalid checksum", func(t *testing.T) {
		origRoot := cacheRootDir
		cacheRootDir = t.TempDir()
		t.Cleanup(func() { cacheRootDir = origRoot })

		badChecksum := "0000000000000000000000000000000000000000000000000000000000000000"
		cleanup := setupServers(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s %s\n", badChecksum, binaryName)
		})
		t.Cleanup(cleanup)

		_, err := Resolve(context.Background(), spec, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")
	})

	t.Run("checksum file unavailable", func(t *testing.T) {
		origRoot := cacheRootDir
		cacheRootDir = t.TempDir()
		t.Cleanup(func() { cacheRootDir = origRoot })

		cleanup := setupServers(t, nil)
		t.Cleanup(cleanup)

		result, err := Resolve(context.Background(), spec, "")
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})
}

func TestChecksumVerificationDifferentBinary(t *testing.T) {
	binaryContent := []byte("different-tool-binary-content")
	tarGzData := createTestTarGz(t, "other-tool", binaryContent)
	correctHash := fmt.Sprintf("%x", sha256.Sum256(binaryContent))

	spec := BinarySpec{
		Name:          "other-tool",
		Owner:         "some-org",
		Repo:          "other-tool-repo",
		AssetPattern:  "{name}-{os}-{arch}.tar.gz",
		ChecksumAsset: "checksums.txt",
	}

	binaryName := binaryAssetName(spec)
	asset := assetName(spec)
	downloadPath := fmt.Sprintf("/some-org/other-tool-repo/releases/download/v2.0.0/%s", asset)
	checksumPath := "/some-org/other-tool-repo/releases/download/v2.0.0/checksums.txt"

	t.Run("picks correct entry from multi-binary checksum file", func(t *testing.T) {
		origRoot := cacheRootDir
		cacheRootDir = t.TempDir()
		t.Cleanup(func() { cacheRootDir = origRoot })

		// Checksum file has entries for multiple binaries — only one matches
		checksumContent := fmt.Sprintf(
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa unrelated-tool-linux-amd64\n"+
				"%s %s\n"+
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb another-tool-darwin-arm64\n",
			correctHash, binaryName)

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/some-org/other-tool-repo/releases/latest" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"tag_name": "v2.0.0"}`)
				return
			}
			http.NotFound(w, r)
		}))
		defer apiServer.Close()

		downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case downloadPath:
				w.Write(tarGzData)
			case checksumPath:
				fmt.Fprint(w, checksumContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer downloadServer.Close()

		origAPI := githubAPIBaseURL
		origDownload := githubDownloadBaseURL
		githubAPIBaseURL = apiServer.URL
		githubDownloadBaseURL = downloadServer.URL
		t.Cleanup(func() {
			githubAPIBaseURL = origAPI
			githubDownloadBaseURL = origDownload
		})

		result, err := Resolve(context.Background(), spec, "")
		require.NoError(t, err)

		content, err := os.ReadFile(result)
		require.NoError(t, err)
		assert.Equal(t, binaryContent, content)
	})

	t.Run("wrong binary detected via checksum", func(t *testing.T) {
		origRoot := cacheRootDir
		cacheRootDir = t.TempDir()
		t.Cleanup(func() { cacheRootDir = origRoot })

		// Checksum for a different binary — should fail
		wrongHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		checksumContent := fmt.Sprintf("%s %s\n", wrongHash, binaryName)

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/some-org/other-tool-repo/releases/latest" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"tag_name": "v2.0.0"}`)
				return
			}
			http.NotFound(w, r)
		}))
		defer apiServer.Close()

		downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case downloadPath:
				w.Write(tarGzData)
			case checksumPath:
				fmt.Fprint(w, checksumContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer downloadServer.Close()

		origAPI := githubAPIBaseURL
		origDownload := githubDownloadBaseURL
		githubAPIBaseURL = apiServer.URL
		githubDownloadBaseURL = downloadServer.URL
		t.Cleanup(func() {
			githubAPIBaseURL = origAPI
			githubDownloadBaseURL = origDownload
		})

		_, err := Resolve(context.Background(), spec, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")
	})
}

func TestResolveSkipsChecksumWhenNotConfigured(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	binaryContent := []byte("no-checksum-binary")
	tarGzData := createTestTarGz(t, "test-binary", binaryContent)

	// Spec with no ChecksumAsset — checksum verification should be skipped entirely
	spec := BinarySpec{
		Name:         "test-binary",
		Owner:        "test-owner",
		Repo:         "test-repo",
		AssetPattern: "{name}-{os}-{arch}.tar.gz",
	}

	asset := assetName(spec)
	downloadPath := fmt.Sprintf("/test-owner/test-repo/releases/download/v1.0.0/%s", asset)

	checksumRequested := false
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-owner/test-repo/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "SHA256") || strings.Contains(r.URL.Path, "checksum") {
			checksumRequested = true
		}
		if r.URL.Path == downloadPath {
			w.Write(tarGzData)
			return
		}
		http.NotFound(w, r)
	}))
	defer downloadServer.Close()

	origAPI := githubAPIBaseURL
	origDownload := githubDownloadBaseURL
	githubAPIBaseURL = apiServer.URL
	githubDownloadBaseURL = downloadServer.URL
	t.Cleanup(func() {
		githubAPIBaseURL = origAPI
		githubDownloadBaseURL = origDownload
	})

	result, err := Resolve(context.Background(), spec, "")
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.False(t, checksumRequested, "checksum file should not be requested when ChecksumAsset is empty")

	content, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, content)
}

func TestFindAnyCachedReturnsLatestVersion(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	spec := BinarySpec{Name: "test-binary"}

	baseDir, err := cacheBaseDir(spec)
	require.NoError(t, err)

	// Create multiple cached versions
	for _, v := range []string{"v0.0.1", "v0.0.2", "v0.0.3"} {
		require.NoError(t, os.MkdirAll(filepath.Join(baseDir, v), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, v, "test-binary"), []byte("bin-"+v), 0755))
	}

	// findAnyCached iterates in reverse (os.ReadDir returns sorted), so should return v0.0.3
	result, err := findAnyCached(spec)
	require.NoError(t, err)
	assert.Contains(t, result, "v0.0.3")

	content, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, []byte("bin-v0.0.3"), content)
}

func TestDownloadReturns404ForUnsupportedPlatform(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	spec := BinarySpec{
		Name:         "test-binary",
		Owner:        "test-owner",
		Repo:         "test-repo",
		AssetPattern: "{name}-{os}-{arch}.tar.gz",
		FlagHint:     "--test-binary-path",
	}

	// Server returns 404 for all download requests (simulates unsupported platform)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-owner/test-repo/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // All downloads return 404
	}))
	defer downloadServer.Close()

	origAPI := githubAPIBaseURL
	origDownload := githubDownloadBaseURL
	githubAPIBaseURL = apiServer.URL
	githubDownloadBaseURL = downloadServer.URL
	t.Cleanup(func() {
		githubAPIBaseURL = origAPI
		githubDownloadBaseURL = origDownload
	})

	_, err := Resolve(context.Background(), spec, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), runtime.GOOS+"/"+runtime.GOARCH)
	assert.Contains(t, err.Error(), "--test-binary-path")
	assert.Contains(t, err.Error(), "https://github.com/test-owner/test-repo/releases")
}

func TestFindAnyCachedNoCacheDir(t *testing.T) {
	spec := BinarySpec{Name: "nonexistent-binary-" + t.Name()}
	_, err := findAnyCached(spec)
	assert.Error(t, err)
}

func TestResolveAutoDownloadZip(t *testing.T) {
	origRoot := cacheRootDir
	cacheRootDir = t.TempDir()
	t.Cleanup(func() { cacheRootDir = origRoot })

	binaryContent := []byte("#!/bin/sh\necho hello from zip\n")
	zipData := createTestZip(t, "test-binary", binaryContent)

	spec := BinarySpec{
		Name:         "test-binary",
		Owner:        "test-owner",
		Repo:         "test-repo",
		AssetPattern: "{name}-{os}-{arch}.zip",
	}

	asset := assetName(spec)
	downloadPath := fmt.Sprintf("/test-owner/test-repo/releases/download/v1.0.0/%s", asset)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-owner/test-repo/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tag_name": "v1.0.0"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == downloadPath {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(zipData)
			return
		}
		http.NotFound(w, r)
	}))
	defer downloadServer.Close()

	origAPI := githubAPIBaseURL
	origDownload := githubDownloadBaseURL
	githubAPIBaseURL = apiServer.URL
	githubDownloadBaseURL = downloadServer.URL
	t.Cleanup(func() {
		githubAPIBaseURL = origAPI
		githubDownloadBaseURL = origDownload
	})

	result, err := Resolve(context.Background(), spec, "")
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	content, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, content)
}

func createTestZip(t *testing.T, binaryName string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	fw, err := zw.Create(binaryName)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.Bytes()
}

func createTestTarGz(t *testing.T, binaryName string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	header := &tar.Header{
		Name: binaryName,
		Mode: 0755,
		Size: int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(header))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	return buf.Bytes()
}
