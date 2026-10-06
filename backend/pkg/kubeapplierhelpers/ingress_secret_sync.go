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

package kubeapplierhelpers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/json"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// IngressSecretSyncDesireName is the ReadDesire/ApplyDesire name for the ingress
// wildcard certificate's SecretSync object (see
// k8sresources.buildIngressCertificateDesires). Defined here rather than in
// k8sresources so this package can look it up without an import cycle.
const IngressSecretSyncDesireName = "IngressCertificateSecretSync"

// GetCachedIngressSecretSyncForCluster reads the ingress wildcard certificate's
// SecretSync object from the cached per-cluster ReadDesire. The bool reports
// whether its Successful condition is true. A nil SecretSync with true means a
// successful read found no object; a missing ReadDesire or an unsuccessful read
// returns false.
//
// Cached content is returned even when the latest read was unsuccessful, so
// callers that only need the last recorded content can ignore the bool.
// Returns an error for non-NotFound lister errors or malformed content.
func GetCachedIngressSecretSyncForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*secretsyncv1alpha1.SecretSync, bool, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, IngressSecretSyncDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get ReadDesire for ingress SecretSync: %w", err))
	}
	observed := meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, observed, nil
	}
	secretSync := &secretsyncv1alpha1.SecretSync{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, secretSync); err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to unmarshal SecretSync from ReadDesire kubeContent: %w", err))
	}
	return secretSync, observed, nil
}
