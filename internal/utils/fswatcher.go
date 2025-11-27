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
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FSWatcher watches a file for content changes using hash-based change detection.
// Changes are detected through periodic checks at the configured interval.
type FSWatcher struct {
	filePath      string
	onChange      func() error
	checkInterval time.Duration
	logger        *slog.Logger
	mu            sync.RWMutex
	fileHash      string
}

// NewFSWatcher creates a new file system watcher that monitors a file for content changes.
//
// Parameters:
//   - filePath: the file to watch (e.g., /mnt/secrets/cert.pem)
//   - checkInterval: how often to check for changes (must be > 0)
//   - onChange: callback invoked when file content changes (required)
//   - opts: optional configuration functions
func NewFSWatcher(filePath string, checkInterval time.Duration, onChange func() error, logger *slog.Logger) (*FSWatcher, error) {
	if onChange == nil {
		return nil, fmt.Errorf("onChange callback is required")
	}
	if checkInterval <= 0 {
		return nil, fmt.Errorf("backstopInterval must be greater than 0")
	}

	watcher := &FSWatcher{
		filePath:      filePath,
		onChange:      onChange,
		checkInterval: checkInterval,
		logger:        logger,
	}

	return watcher, nil
}

// Start begins watching the file for content changes.
//
// The initial file hash is computed before starting the background watcher to ensure
// the file exists and is readable. The watcher then runs in a background goroutine
// until ctx is canceled.
//
// Changes are detected through periodic hash checks at the configured checkInterval.
// When a change is detected, the onChange callback is invoked.
func (w *FSWatcher) Start(ctx context.Context) error {
	// Verify the file exists and compute initial hash
	hash, err := w.hashFile(w.filePath)
	if err != nil {
		return fmt.Errorf("failed to hash file at %s: %w", w.filePath, err)
	}

	w.mu.Lock()
	w.fileHash = hash
	w.mu.Unlock()

	w.logger.Info("starting file watcher", "filePath", w.filePath, "checkInterval", w.checkInterval)

	go w.watchLoop(ctx)

	return nil
}

// hashFile computes the SHA256 hash of a file's contents
func (w *FSWatcher) hashFile(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// checkAndSignal checks if the file has been modified and invokes the callback if so
func (w *FSWatcher) checkAndSignal() {
	newHash, err := w.hashFile(w.filePath)
	if err != nil {
		w.logger.Error(fmt.Errorf("failed to hash file during modification check: %w", err).Error())
		return
	}

	w.mu.Lock()
	currentHash := w.fileHash
	if newHash != currentHash {
		w.fileHash = newHash
		w.mu.Unlock()

		w.logger.Info("detected file content change", "path", w.filePath)
		if err := w.onChange(); err != nil {
			w.logger.Error(fmt.Errorf("onChange callback failed: %w", err).Error())
		}
		return
	}
	w.mu.Unlock()
}

// watchLoop periodically checks for file changes and invokes the callback if so
func (w *FSWatcher) watchLoop(ctx context.Context) {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {

		case <-ticker.C:
			w.checkAndSignal()

		case <-ctx.Done():
			w.logger.Info("stopping file watcher", "reason", ctx.Err())
			return
		}
	}
}
