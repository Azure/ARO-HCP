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

package fpa

import (
	"context"
	"crypto"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type FirstPartyApplicationTokenCredentialRetriever interface {
	RetrieveCredential(tenantId string, additionallyAllowedTenants ...string) (azcore.TokenCredential, error)
	NotBefore() time.Time
}

type firstPartyApplicationTokenCredentialRetriever struct {
	clientOpts        azcore.ClientOptions
	clientID          string
	credentialFile    string
	certificateBundle *certificateBundle
	lock              *sync.RWMutex
	logger            *slog.Logger
	checkInterval     time.Duration
}

type certificateBundle struct {
	certificates []*x509.Certificate
	privateKey   crypto.PrivateKey
}

func NewFirstPartyApplicationTokenCredentialRetriever(ctx context.Context, logger *slog.Logger, clientID, credentialPath string, clientOptions azcore.ClientOptions, checkInterval time.Duration) (FirstPartyApplicationTokenCredentialRetriever, error) {
	credentialRetriever := &firstPartyApplicationTokenCredentialRetriever{
		clientID:       clientID,
		lock:           &sync.RWMutex{},
		logger:         logger,
		checkInterval:  checkInterval,
		credentialFile: credentialPath,
		clientOpts:     clientOptions,
	}

	// load once to validate everything and ensure we have a useful token before we return
	if err := credentialRetriever.load(); err != nil {
		return nil, err
	}
	// start the process of watching - the caller can cancel ctx if they want to stop
	if err := credentialRetriever.start(ctx); err != nil {
		return nil, err
	}
	return credentialRetriever, nil
}

func (r *firstPartyApplicationTokenCredentialRetriever) RetrieveCredential(tenantId string, additionallyAllowedTenants ...string) (azcore.TokenCredential, error) {
	options := &azidentity.ClientCertificateCredentialOptions{
		SendCertificateChain:       true,
		AdditionallyAllowedTenants: additionallyAllowedTenants,
		ClientOptions:              r.clientOpts,
	}
	bundle := r.certificateBundle
	return azidentity.NewClientCertificateCredential(
		tenantId,
		r.clientID,
		bundle.certificates,
		bundle.privateKey,
		options,
	)
}

// NotBefore returns the NotBefore timestamp of the currently loaded certificate.
func (r *firstPartyApplicationTokenCredentialRetriever) NotBefore() time.Time {
	return r.certificateBundle.certificates[0].NotBefore
}

func (r *firstPartyApplicationTokenCredentialRetriever) start(ctx context.Context) error {
	// Create file system watcher that will call r.load when the file changes
	watcher, err := utils.NewFSWatcher(
		r.credentialFile,
		r.checkInterval,
		r.load,
		r.logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Start watching for file changes
	// The watcher will run until ctx is canceled
	if err := watcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}

	return nil
}

func (r *firstPartyApplicationTokenCredentialRetriever) load() error {
	// Read the PEM certificate bundle from the filesystem
	pemData, err := os.ReadFile(r.credentialFile)
	if err != nil {
		return fmt.Errorf("failed to read credential file %s: %w", r.credentialFile, err)
	}

	// Parse the PEM certificate bundle (contains both private key and certificate)
	certs, key, err := azidentity.ParseCertificates(pemData, nil)
	if err != nil {
		return fmt.Errorf("failed to parse PEM certificate bundle from %s: %w", r.credentialFile, err)
	}

	if len(certs) == 0 {
		return fmt.Errorf("no certificates found in %s", r.credentialFile)
	}

	r.lock.Lock()
	defer r.lock.Unlock()
	r.certificateBundle = &certificateBundle{
		certificates: certs,
		privateKey:   key,
	}
	r.logger.Info("successfully loaded new certificate",
		"notBefore", certs[0].NotBefore.Format(time.RFC3339),
		"notAfter", certs[0].NotAfter.Format(time.RFC3339))

	return nil
}
