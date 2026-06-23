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

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/util/json"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameForSystemAdminCredentialCSR returns the well-known
// ReadDesire name used to mirror the per-credential
// CertificateSigningRequest. The credential name is the suffix; see
// systemadmincredential.CSRNamePrefix.
func ReadDesireNameForSystemAdminCredentialCSR(credentialName string) string {
	return systemadmincredential.CSRNamePrefix + "-" + credentialName
}

// ReadDesireNameForSystemAdminCredentialRevocation returns the
// well-known ReadDesire name used to mirror the per-revoke
// CertificateRevocationRequest. The revoke-op suffix is the suffix; see
// systemadmincredential.CRRNamePrefix.
func ReadDesireNameForSystemAdminCredentialRevocation(revokeOpSuffix string) string {
	return systemadmincredential.CRRNamePrefix + "-" + revokeOpSuffix
}

// GetCachedCSRForSystemAdminCredential reads the per-credential CSR
// mirror from the per-cluster ReadDesire keyed under the per-credential
// name. The ReadDesire's Status.KubeContent.Raw carries the observed
// CertificateSigningRequest JSON; we decode it directly and return the
// typed object.
//
// Returns (nil, nil) when:
//   - the ReadDesire has not been created yet (NotFound),
//   - the ReadDesire exists but the kube-applier has not yet observed
//     the target (Status.KubeContent is nil or empty).
//
// Returns a non-nil error only for hard failures: a non-NotFound lister
// error, or unmarshal failure.
func GetCachedCSRForSystemAdminCredential(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, credentialName string,
) (*certificatesv1.CertificateSigningRequest, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName,
		ReadDesireNameForSystemAdminCredentialCSR(credentialName))
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for SystemAdminCredential CSR: %w", err))
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

// GetCachedCertificateRevocationRequestForCluster reads the
// CertificateRevocationRequest mirror from the per-cluster ReadDesire
// keyed under the per-revoke name. Same semantics as
// GetCachedCSRForSystemAdminCredential.
func GetCachedCertificateRevocationRequestForCluster(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, revokeOpSuffix string,
) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName,
		ReadDesireNameForSystemAdminCredentialRevocation(revokeOpSuffix))
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
