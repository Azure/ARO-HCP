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
	"fmt"

	"github.com/Azure/ARO-HCP/internal/fpa"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type ARMHelperTokenCredentialRetriever interface {
	// We only pass the tenant because in some envs the HCP Clusters are in
	// a different tenant than where the service is deployed. If we want to avoid
	// that we could pass it via cli flag to indicate the tenant of the arm helper identity
	// lives.
	RetrieveCredential(tenantID string) (azcore.TokenCredential, error)
}

type armHelperTokenCredentialRetriever struct {
	options    *azcore.ClientOptions
	clientID   string
	certReader fpa.CertificateReader
}

func (r *armHelperTokenCredentialRetriever) RetrieveCredential(tenantID string) (azcore.TokenCredential, error) {
	certs, key, err := r.certReader.ReadCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	clientCertificateCredentialOptions := &azidentity.ClientCertificateCredentialOptions{
		SendCertificateChain: true,
		ClientOptions:        *r.options,
	}

	return azidentity.NewClientCertificateCredential(tenantID, r.clientID, certs, key, clientCertificateCredentialOptions)
}

// TODO move fpa.CertificateReader interface to a more common package as it's not particular to FPA
func NewARMHelperTokenCredentialRetriever(
	clientID string, certReader fpa.CertificateReader, options *azcore.ClientOptions,
) (ARMHelperTokenCredentialRetriever, error) {
	credentialRetriever := &armHelperTokenCredentialRetriever{
		clientID:   clientID,
		certReader: certReader,
		options:    options,
	}

	// Validate we can read the certificate
	_, _, err := certReader.ReadCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to read initial certificate: %w", err)
	}

	return credentialRetriever, nil
}

// TODO figure out that fits with Check Access V2 interaction
// TODO figure out how we would run ARM Helper: A controller that only runs
// depending on whether ARM Helper CLI flags have been set? The controllers
// would also need to conditionally wait until the ARM Helper action of MRG
// MRG permissions is performed but only in environments where a real FPA is
// not available.
