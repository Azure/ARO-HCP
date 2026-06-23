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

package maestrohelpers

import (
	"context"
	"fmt"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// GetCachedCSRForSystemAdminCredential reads a CSR mirror from a
// per-credential ReadDesire. The ReadDesire's Status.KubeContent.Raw
// carries the observed CSR JSON.
//
// Returns (nil, nil) when the ReadDesire has not been created yet or
// the kube-applier has not yet observed the target.
func GetCachedCSRForSystemAdminCredential(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, desireName string,
) (*certificatesv1.CertificateSigningRequest, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, desireName)
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
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal CSR from ReadDesire kubeContent: %w", err))
	}
	return csr, nil
}

// GetCachedCertificateRevocationRequestForCluster reads a CRR mirror
// from a cluster-scoped ReadDesire. Returns the CRR as an
// Unstructured object since the HyperShift CRR type may not be
// available in all import contexts.
//
// Returns (nil, nil) when the ReadDesire has not been created yet or
// the kube-applier has not yet observed the target.
func GetCachedCertificateRevocationRequestForCluster(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, desireName string,
) (*unstructured.Unstructured, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, desireName)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for CRR: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	crr := &unstructured.Unstructured{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, &crr.Object); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal CRR from ReadDesire kubeContent: %w", err))
	}
	return crr, nil
}
