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
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

// BinarySpec defines a downloadable binary from a GitHub release.
// Currently only GitHub releases are supported. To add other sources,
// this would need to be extended with a source-specific interface.
type BinarySpec struct {
	Name                string // binary name, e.g. "must-gather-clean"
	Owner               string // GitHub organization, e.g. "openshift"
	Repo                string // GitHub repository, e.g. "must-gather-clean"
	AssetPattern        string // template for non-Windows: "{name}-{os}-{arch}.tar.gz"
	WindowsAssetPattern string // template for Windows: "{name}-{os}-{arch}.exe.zip"
	FlagHint            string // used in error messages
	ChecksumAsset       string // release asset containing SHA256 checksums (e.g. "SHA256_SUM")
}

func (s BinarySpec) flagHint() string {
	if s.FlagHint != "" {
		return s.FlagHint
	}
	return "--<binary-path>"
}

const githubHTTPTimeout = 60 * time.Second

var httpClient = &http.Client{Timeout: githubHTTPTimeout}

var githubAPIBaseURL = "https://api.github.com"
var githubDownloadBaseURL = "https://github.com"

type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
}

// Resolve returns the path to the binary. It checks in order:
// 1. An explicit path provided by the user (verified to exist).
// 2. The latest GitHub release version, using a locally cached copy if available.
// 3. A previously cached version as a fallback when GitHub is unreachable.
// If no cached binary exists, the latest release is downloaded and cached.
func Resolve(ctx context.Context, spec BinarySpec, explicitPath string) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("failed to find binary at explicit path %q: %w", explicitPath, err)
		}
		logger.V(1).Info("using explicit binary path", "path", explicitPath)
		return explicitPath, nil
	}

	version, err := getLatestVersion(ctx, spec)
	if err != nil {
		logger.V(1).Info("failed to query GitHub for latest version, attempting cache fallback", "error", err)
		cached, fallbackErr := findAnyCached(spec)
		if fallbackErr != nil {
			return "", fmt.Errorf("failed to get latest version from GitHub and no cached binary found; "+
				"you can manually download the binary and provide it via %s: %w", spec.flagHint(), err)
		}
		logger.V(1).Info("using cached binary (may be outdated)", "path", cached)
		return cached, nil
	}

	binPath, err := cachedBinaryPath(spec, version)
	if err != nil {
		return "", fmt.Errorf("failed to determine cache path; "+
			"you can manually provide the binary via %s: %w", spec.flagHint(), err)
	}

	if _, err := os.Stat(binPath); err == nil {
		logger.V(4).Info("using cached binary", "path", binPath, "version", version)
		return binPath, nil
	}

	logger.V(1).Info("downloading binary", "name", spec.Name, "version", version)
	if err := download(ctx, spec, version, binPath); err != nil {
		return "", fmt.Errorf("failed to download %s %s for %s/%s; "+
			"you can manually download from https://github.com/%s/%s/releases and provide it via %s: %w",
			spec.Name, version, runtime.GOOS, runtime.GOARCH, spec.Owner, spec.Repo, spec.flagHint(), err)
	}

	if err := cleanOldVersions(spec, version); err != nil {
		logger.V(1).Info("failed to clean old cached versions", "error", err)
	}

	logger.V(1).Info("binary downloaded and cached", "path", binPath, "version", version)
	return binPath, nil
}

func getLatestVersion(ctx context.Context, spec BinarySpec) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBaseURL, spec.Owner, spec.Repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to decode GitHub API response: %w", err)
	}

	if release.TagName == "" {
		return "", fmt.Errorf("GitHub API returned empty tag name")
	}

	return release.TagName, nil
}

func download(ctx context.Context, spec BinarySpec, version, destPath string) error {
	asset := assetName(spec)
	url := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", githubDownloadBaseURL, spec.Owner, spec.Repo, version, asset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d for %s", resp.StatusCode, url)
	}

	tmpFile, err := os.CreateTemp("", "bincache-download-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write downloaded asset: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to flush downloaded asset to disk: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory %q (check write permissions): %w", filepath.Dir(destPath), err)
	}

	if strings.HasSuffix(asset, ".tar.gz") {
		if err := extractTarGz(tmpFile.Name(), spec.Name, destPath); err != nil {
			return fmt.Errorf("failed to extract tar.gz archive: %w", err)
		}
	} else if strings.HasSuffix(asset, ".zip") {
		if err := extractZip(tmpFile.Name(), spec.Name, destPath); err != nil {
			return fmt.Errorf("failed to extract zip archive: %w", err)
		}
	} else {
		return fmt.Errorf("unsupported asset format: %s", asset)
	}

	if err := verifyChecksum(ctx, spec, version, destPath); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	return nil
}

const maxBinarySize = 256 << 20 // 256 MB

func writeExtractedBinary(src io.Reader, binaryName, destPath string) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create binary file: %w", err)
	}

	n, copyErr := io.Copy(out, io.LimitReader(src, maxBinarySize+1))
	if closeErr := out.Close(); closeErr != nil && copyErr == nil {
		return fmt.Errorf("failed to close extracted binary: %w", closeErr)
	}
	if copyErr != nil {
		return fmt.Errorf("failed to extract binary: %w", copyErr)
	}
	if n > maxBinarySize {
		os.Remove(destPath)
		return fmt.Errorf("binary %q exceeds maximum allowed size (%d MB)", binaryName, maxBinarySize>>20)
	}
	return nil
}

func extractTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		if filepath.Base(header.Name) == binaryName && header.Typeflag == tar.TypeReg {
			return writeExtractedBinary(tr, binaryName, destPath)
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractZip(archivePath, binaryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer r.Close()

	winBinaryName := binaryName + ".exe"
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if name != binaryName && name != winBinaryName {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip entry: %w", err)
		}
		defer rc.Close()

		return writeExtractedBinary(rc, binaryName, destPath)
	}

	return fmt.Errorf("binary %q not found in zip archive", binaryName)
}

func binaryAssetName(spec BinarySpec) string {
	asset := assetName(spec)
	asset = strings.TrimSuffix(asset, ".tar.gz")
	asset = strings.TrimSuffix(asset, ".zip")
	return asset
}

func verifyChecksum(ctx context.Context, spec BinarySpec, version, binaryPath string) error {
	if spec.ChecksumAsset == "" {
		return nil
	}

	logger := logr.FromContextOrDiscard(ctx)

	url := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s",
		githubDownloadBaseURL, spec.Owner, spec.Repo, version, spec.ChecksumAsset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logger.V(1).Info("failed to create checksum request, skipping verification", "error", err)
		return nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		logger.V(1).Info("failed to download checksum file, skipping verification", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.V(1).Info("checksum file not available, skipping verification", "status", resp.StatusCode)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.V(1).Info("failed to read checksum file, skipping verification", "error", err)
		return nil
	}

	targetName := binaryAssetName(spec)
	var expectedHash string
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == targetName {
			expectedHash = parts[0]
			break
		}
	}

	if expectedHash == "" {
		logger.V(1).Info("binary not found in checksum file, skipping verification", "binary", targetName)
		return nil
	}

	f, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open binary for checksum verification: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	actualHash := fmt.Sprintf("%x", h.Sum(nil))
	if actualHash != expectedHash {
		os.Remove(binaryPath)
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", spec.Name, expectedHash, actualHash)
	}

	logger.V(4).Info("checksum verified", "binary", targetName, "sha256", actualHash)
	return nil
}

func assetName(spec BinarySpec) string {
	pattern := spec.AssetPattern
	if runtime.GOOS == "windows" && spec.WindowsAssetPattern != "" {
		pattern = spec.WindowsAssetPattern
	}
	r := strings.NewReplacer(
		"{name}", spec.Name,
		"{os}", runtime.GOOS,
		"{arch}", runtime.GOARCH,
	)
	return r.Replace(pattern)
}

// cacheRootDir can be overridden in tests
var cacheRootDir = ""

func cacheBaseDir(spec BinarySpec) (string, error) {
	root := cacheRootDir
	if root == "" {
		var err error
		root, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("failed to determine user cache directory: %w", err)
		}
		root = filepath.Join(root, "hcpctl", "bin")
	}
	return filepath.Join(root, spec.Name), nil
}

func cachedBinaryPath(spec BinarySpec, version string) (string, error) {
	baseDir, err := cacheBaseDir(spec)
	if err != nil {
		return "", err
	}
	binaryName := spec.Name
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	return filepath.Join(baseDir, version, binaryName), nil
}

func cleanOldVersions(spec BinarySpec, currentVersion string) error {
	baseDir, err := cacheBaseDir(spec)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	var errs []error
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != currentVersion {
			if err := os.RemoveAll(filepath.Join(baseDir, entry.Name())); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove old version %s: %w", entry.Name(), err))
			}
		}
	}

	return errors.Join(errs...)
}

func findAnyCached(spec BinarySpec) (string, error) {
	baseDir, err := cacheBaseDir(spec)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to read cache directory: %w", err)
	}

	binaryName := spec.Name
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	for _, entry := range slices.Backward(entries) {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(baseDir, entry.Name(), binaryName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no cached binary found for %s", spec.Name)
}
