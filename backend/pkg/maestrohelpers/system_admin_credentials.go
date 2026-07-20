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

package maestrohelpers

import (
	"context"
	"fmt"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/util/json"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameForSystemAdminCredentialRequestCSR returns the ReadDesire name for a
// per-credential CSR mirror, keyed by the credential's short name.
func ReadDesireNameForSystemAdminCredentialRequestCSR(credName string) string {
	return strings.ToLower(fmt.Sprintf("systemAdminCredentialCSR-%s", credName))
}

// ReadDesireNameForSystemAdminCredentialRequestRevocation returns the ReadDesire name
// for a per-operation CRR mirror, keyed by the revoke operation's suffix.
func ReadDesireNameForSystemAdminCredentialRequestRevocation(revokeOpSuffix string) string {
	return strings.ToLower(fmt.Sprintf("systemAdminCredentialRevocation-%s", revokeOpSuffix))
}

// GetCachedCSRForSystemAdminCredentialRequest reads the CSR mirror from the
// credential-request-scoped ReadDesire (nested under the
// SystemAdminCredentialRequest named by credName). The ReadDesire's
// Status.KubeContent.Raw carries the observed CertificateSigningRequest JSON; we
// decode it directly and return the typed object.
//
// Returns (nil, nil) when:
//   - the ReadDesire has not been created yet (NotFound),
//   - the ReadDesire exists but the kube-applier has not yet observed
//     the target (Status.KubeContent is nil or empty).
//
// Returns a non-nil error only for hard failures: a non-NotFound lister
// error, or unmarshal failure.
func GetCachedCSRForSystemAdminCredentialRequest(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, credName string,
) (*certificatesv1.CertificateSigningRequest, error) {
	desireName := ReadDesireNameForSystemAdminCredentialRequestCSR(credName)
	readDesire, err := readDesireLister.GetForCredentialRequest(ctx, subscriptionName, resourceGroupName, clusterName, credName, desireName)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for CSR: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	csr := &certificatesv1.CertificateSigningRequest{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, csr); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal CertificateSigningRequest from ReadDesire kubeContent: %w", err))
	}
	return csr, nil
}

// GetCachedCertificateRevocationRequestForRevocation reads the CRR mirror from
// the revocation-scoped ReadDesire (nested under the
// SystemAdminCredentialRevocation named by revocationName). The ReadDesire's
// Status.KubeContent.Raw carries the observed CertificateRevocationRequest JSON;
// we decode it directly and return the typed object.
//
// Returns (nil, nil) when:
//   - the ReadDesire has not been created yet (NotFound),
//   - the ReadDesire exists but the kube-applier has not yet observed
//     the target (Status.KubeContent is nil or empty).
//
// Returns a non-nil error only for hard failures: a non-NotFound lister
// error, or unmarshal failure.
func GetCachedCertificateRevocationRequestForRevocation(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, revocationName, revokeOpSuffix string,
) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
	desireName := ReadDesireNameForSystemAdminCredentialRequestRevocation(revokeOpSuffix)
	readDesire, err := readDesireLister.GetForRevocation(ctx, subscriptionName, resourceGroupName, clusterName, revocationName, desireName)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for CertificateRevocationRequest: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	crr := &certificatesv1alpha1.CertificateRevocationRequest{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, crr); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal CertificateRevocationRequest from ReadDesire kubeContent: %w", err))
	}
	return crr, nil
}
