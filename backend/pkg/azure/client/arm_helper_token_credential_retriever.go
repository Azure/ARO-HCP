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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/certificate"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TODO the ARM Helper identity is used in the ARO-HCP environments where we don't have a real FPA available. In those environments,
// we provide the ARM Helper Identity information via CLI flags to the deployment. The presence of the ARM Helper identity CLI flags
// is used as a signal that a real FPA is not available.
// At the moment of writing this (2025-03-10) the tasks that leverage the ARM Helper identity are:
//   - Validations that need to leverage the Check Access V2 API: In the environments where a real FPA is not available
//     we use the ARM Helper identity to authenticate against the check access v2 api. These validations need to have the CheckAccessV2 client.
//   - The Cluster provision step that gives `Owner` permission to the mock FPA identity, over the cluster being provisioned. This simulates what
//     ARM would do transparently to the real FPA. This step is only performed in the aro-hcp environments where a real FPA is not available.
//
// Additionally, the Cluster provision step that creates the Azure DenyAssignments associated with the cluster is only executed
// when a real FPA is not available. This is, when the ARM Helper Identity CLI flags are being set.
//
// ARMHelperIdentityTokenCredentialRetriever is an interface that retrieves a token credential for the ARM Helper identity.
type ARMHelperIdentityTokenCredentialRetriever interface {
	RetrieveCredential() (azcore.TokenCredential, error)
}

type armHelperIdentityTokenCredentialRetriever struct {
	options *azcore.ClientOptions
	// tenantID is the tenant id of the ARM Helper identity.
	tenantID string
	// clientID is the client id of the ARM Helper identity.
	clientID string
	// certReader is the reader for the certificate bundle of the ARM Helper identity.
	certReader certificate.Reader
}

func (r *armHelperIdentityTokenCredentialRetriever) RetrieveCredential() (azcore.TokenCredential, error) {
	certs, key, err := r.certReader.ReadCertificate()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to read certificate: %w", err))
	}

	clientCertificateCredentialOptions := &azidentity.ClientCertificateCredentialOptions{
		SendCertificateChain: true,
		ClientOptions:        *r.options,
	}

	cred, err := azidentity.NewClientCertificateCredential(r.tenantID, r.clientID, certs, key, clientCertificateCredentialOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create ARM Helper identity token credential: %w", err))
	}

	return cred, nil
}

func NewARMHelperIdentityTokenCredentialRetriever(tenantID string, clientID string, certReader certificate.Reader, options *azcore.ClientOptions,
) (ARMHelperIdentityTokenCredentialRetriever, error) {
	credentialRetriever := &armHelperIdentityTokenCredentialRetriever{
		tenantID:   tenantID,
		clientID:   clientID,
		certReader: certReader,
		options:    options,
	}

	// Validate we can read the certificate
	_, _, err := certReader.ReadCertificate()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to read initial certificate: %w", err))
	}

	return credentialRetriever, nil
}
