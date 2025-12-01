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
	"fmt"
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type FirstPartyApplicationTokenCredentialRetriever interface {
	RetrieveCredential(tenantId string, additionallyAllowedTenants ...string) (azcore.TokenCredential, error)
}

type firstPartyApplicationTokenCredentialRetriever struct {
	clientOpts azcore.ClientOptions
	clientID   string
	certReader CertificateReader
	logger     *slog.Logger
}

func NewFirstPartyApplicationTokenCredentialRetriever(logger *slog.Logger, clientID string, certReader CertificateReader, clientOptions azcore.ClientOptions) (FirstPartyApplicationTokenCredentialRetriever, error) {
	credentialRetriever := &firstPartyApplicationTokenCredentialRetriever{
		clientID:   clientID,
		logger:     logger,
		certReader: certReader,
		clientOpts: clientOptions,
	}

	// Validate we can read the certificate
	_, _, err := certReader.ReadCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to read initial certificate: %w", err)
	}

	return credentialRetriever, nil
}

func (r *firstPartyApplicationTokenCredentialRetriever) RetrieveCredential(tenantId string, additionallyAllowedTenants ...string) (azcore.TokenCredential, error) {
	certs, key, err := r.certReader.ReadCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	options := &azidentity.ClientCertificateCredentialOptions{
		SendCertificateChain:       true,
		AdditionallyAllowedTenants: additionallyAllowedTenants,
		ClientOptions:              r.clientOpts,
	}

	return azidentity.NewClientCertificateCredential(
		tenantId,
		r.clientID,
		certs,
		key,
		options,
	)
}
