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

package certificate

import (
	"context"
	"crypto"
	"crypto/x509"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// WatchingAzureIdentityFileReader wraps AzureIdentityFileReader with caching and automatic reloading.
// It watches the certificate file and reloads when changes are detected.
type WatchingAzureIdentityFileReader struct {
	reader   *AzureIdentityFileReader
	filePath string

	mu sync.RWMutex
	// bundle, certs and key are all derived from the same read of the
	// underlying file and are always updated together, under mu, by reload:
	// bundle is the raw file content (PEM or PKCS#12, whatever format it was
	// stored in), and certs/key are the certificate chain and private key
	// obtained by parsing bundle. Keeping all three in sync means ReadBundle
	// and ReadCertificate never observe a torn combination, e.g. certs/key
	// from one file revision paired with bundle from another.
	bundle []byte
	certs  []*x509.Certificate
	key    crypto.PrivateKey
}

var (
	_ Reader       = (*WatchingAzureIdentityFileReader)(nil)
	_ BundleReader = (*WatchingAzureIdentityFileReader)(nil)
)

// NewWatchingAzureIdentityFileReader creates a new watching certificate reader.
// It loads the initial certificate and starts watching for changes.
// The logger is obtained from the context using utils.LoggerFromContext.
func NewWatchingAzureIdentityFileReader(ctx context.Context, filePath string) (*WatchingAzureIdentityFileReader, error) {
	reader := NewAzureIdentityFileReader(filePath)

	w := &WatchingAzureIdentityFileReader{
		reader:   reader,
		filePath: filePath,
	}

	// Load initial certificate
	if err := w.reload(ctx); err != nil {
		return nil, fmt.Errorf("failed to load initial certificate: %w", err)
	}

	return w, nil
}

// Run starts watching the certificate file for changes.
// When changes are detected, the reload callback is invoked.
// Watching continues until the context is canceled.
func (w *WatchingAzureIdentityFileReader) Run(ctx context.Context, checkInterval time.Duration) error {
	watcher, err := utils.NewFSWatcher(w.filePath, checkInterval, w.reload)
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	return watcher.Start(ctx)
}

// ReadCertificate returns the cached, parsed certificate and private key.
func (w *WatchingAzureIdentityFileReader) ReadCertificate() ([]*x509.Certificate, crypto.PrivateKey, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.certs, w.key, nil
}

// ReadBundle returns the cached, raw certificate bundle bytes, in whatever
// format (PEM or PKCS#12) they were originally stored in.
func (w *WatchingAzureIdentityFileReader) ReadBundle() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.bundle, nil
}

// reload reads and caches the certificate bundle, and its parsed form, from
// the underlying reader.
func (w *WatchingAzureIdentityFileReader) reload(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	bundle, err := w.reader.ReadBundle()
	if err != nil {
		return fmt.Errorf("failed to read certificate bundle: %w", err)
	}

	certs, key, err := azidentity.ParseCertificates(bundle, nil)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.bundle = bundle
	w.certs = certs
	w.key = key

	logger.Info("certificate reloaded",
		"notBefore", certs[0].NotBefore.Format(time.RFC3339),
		"notAfter", certs[0].NotAfter.Format(time.RFC3339))

	return nil
}
