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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ReadDesireNameServingCA = "systemadmincredential-serving-ca"

// GetCachedServingCASecretForCluster reads the serving CA Secret mirror
// from the per-cluster ReadDesire.
//
// Returns (nil, nil) when:
//   - the ReadDesire has not been created yet (NotFound),
//   - the ReadDesire exists but the kube-applier has not yet observed
//     the target (Status.KubeContent is nil or empty).
func GetCachedServingCASecretForCluster(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*corev1.Secret, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameServingCA)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for serving CA: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	secret := &corev1.Secret{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, secret); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal Secret from ReadDesire kubeContent: %w", err))
	}
	return secret, nil
}
