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

// Package cache provides a file-based cache for GCS artifacts.
// GCS artifacts for finished Prow jobs are immutable — once written, they
// never change. This means we can cache them indefinitely without TTL.
package cache

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the default cache directory.
func Dir() string {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "ci-triage", "gcs")
}

// FileCache stores fetched GCS objects on disk, keyed by URL.
type FileCache struct {
	dir string
}

// New creates a FileCache at the given directory.
func New(dir string) *FileCache {
	return &FileCache{dir: dir}
}

// Get returns cached data for a URL, or nil if not cached.
func (c *FileCache) Get(url string) []byte {
	path := c.path(url)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// Set stores data for a URL in the cache.
func (c *FileCache) Set(url string, data []byte) {
	path := c.path(url)
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// path returns the filesystem path for a cached URL.
// Uses SHA256 hash split into 2-char prefix for directory distribution.
func (c *FileCache) path(url string) string {
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	return filepath.Join(c.dir, h[:2], h+".dat")
}
