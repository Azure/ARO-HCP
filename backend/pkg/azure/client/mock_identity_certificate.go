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

package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type mockIdentityCertificateGeneration struct {
	digest     [sha256.Size]byte
	bundle     string
	credential *azidentity.ClientCertificateCredential
}

type mockIdentityCertificate struct {
	path, clientID, tenantID string
	options                  azcore.ClientOptions
	reloadMu                 sync.Mutex
	generation               atomic.Pointer[mockIdentityCertificateGeneration]
}

// NewHardcodedIdentity watches the mock certificate while keeping identity metadata fixed.
func NewHardcodedIdentity(ctx context.Context, path, clientID, principalID, tenantID string, options azcore.ClientOptions) (*HardcodedIdentity, error) {
	certificate, err := newMockIdentityCertificate(ctx, path, clientID, tenantID, options, time.Minute)
	if err != nil {
		return nil, err
	}
	return &HardcodedIdentity{ClientID: clientID, PrincipalID: principalID, TenantID: tenantID, certificate: certificate}, nil
}

// Creates the watcher baseline before loading the first credential generation.
func newMockIdentityCertificate(ctx context.Context, path, clientID, tenantID string, options azcore.ClientOptions, interval time.Duration) (*mockIdentityCertificate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	certificate := &mockIdentityCertificate{path: path, clientID: clientID, tenantID: tenantID, options: options}
	watcher, err := utils.NewFSWatcher(path, interval, certificate.reload)
	if err != nil {
		return nil, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	if err = watcher.Start(watchCtx); err != nil {
		cancel()
		return nil, err
	}
	if err = certificate.reload(watchCtx); err != nil {
		cancel()
		return nil, err
	}
	return certificate, nil
}

// Publishes a complete valid generation without discarding the last working credential.
func (c *mockIdentityCertificate) reload(ctx context.Context) error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()
	bundle, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("reading mock MSI certificate: %w", err)
	}
	digest := sha256.Sum256(bundle)
	if old := c.generation.Load(); old != nil && old.digest == digest {
		return nil
	}
	certs, key, err := azidentity.ParseCertificates(bundle, nil)
	if err != nil {
		return fmt.Errorf("parsing mock MSI certificate: %w", err)
	}
	// Match dataplane.GetCredential's MSI authentication options.
	credential, err := azidentity.NewClientCertificateCredential(c.tenantID, c.clientID, certs, key, &azidentity.ClientCertificateCredentialOptions{
		ClientOptions:            c.options,
		SendCertificateChain:     true,
		DisableInstanceDiscovery: true,
	})
	if err != nil {
		return fmt.Errorf("creating mock MSI credential: %w", err)
	}
	c.generation.Store(&mockIdentityCertificateGeneration{digest: digest, bundle: base64.StdEncoding.EncodeToString(bundle), credential: credential})
	return nil
}

// GetToken uses the current generation even for clients constructed before rotation.
func (c *mockIdentityCertificate) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return c.generation.Load().credential.GetToken(ctx, options)
}

// Returns the live mock credential without converting a synthetic century-long response into a snapshot.
func (c *hardcodedIdentityManagedIdentitiesDataplaneClient) tokenCredential() (azcore.TokenCredential, error) {
	if c.hardcodedIdentity.certificate == nil {
		return nil, fmt.Errorf("mock MSI certificate is not initialized")
	}
	return c.hardcodedIdentity.certificate, nil
}

// Identifies mock dataplane clients that expose a rotating token credential directly.
type rotatingTokenCredentialSource interface {
	tokenCredential() (azcore.TokenCredential, error)
}
