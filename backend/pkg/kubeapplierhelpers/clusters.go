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
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameReadonlyHostedCluster is the well-known ReadDesire name the
// backend writes the per-cluster HostedCluster mirror under. Consumers look
// this up via a ReadDesireLister.GetForCluster call.
//
// The value is the lowercased form of
// coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster — the same
// derivation the writer (create_cluster_scoped_read_desires_controller.go)
// uses for its desired ReadDesire name. Lowercased so the resourceID path
// reduces to a stable Cosmos key regardless of case.
var ReadDesireNameReadonlyHostedCluster = strings.ToLower(string(coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))

// GetCachedHostedClusterForCluster reads the HostedCluster from the cached
// per-cluster ReadDesire. The bool reports whether its Successful condition is
// true. A nil HostedCluster with true means a successful read found no object;
// a missing ReadDesire or an unsuccessful read returns false.
//
// Cached content is returned even when the latest read was unsuccessful, so
// callers that only need the last recorded content can ignore the bool.
// Returns an error for non-NotFound lister errors or malformed content.
func GetCachedHostedClusterForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*v1beta1.HostedCluster, bool, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameReadonlyHostedCluster)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get ReadDesire for HostedCluster: %w", err))
	}
	observed := meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, observed, nil
	}
	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, hostedCluster); err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to unmarshal HostedCluster from ReadDesire kubeContent: %w", err))
	}
	return hostedCluster, observed, nil
}

// GetCachedHostedClusterUUIDForCluster resolves the cluster UUID parsed from the cached HostedCluster's
// Spec.ClusterID for the given cluster.
//
// Returns (uuid, true, nil) on success.
//
// Returns (uuid.Nil, false, nil) for transient situations the caller should treat as a silent skip:
// the ReadDesire has not been observed yet, the kubeContent does not yet hold the HostedCluster,
// or the HostedCluster's Spec.ClusterID is empty. The reason is logged via the context logger so
// callers don't need to.
//
// Returns a non-nil error only for hard failures: a non-NotFound lister error, malformed kubecontent,
// or an unparseable UUID.
func GetCachedHostedClusterUUIDForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (uuid.UUID, bool, error) {
	logger := utils.LoggerFromContext(ctx)
	hostedCluster, _, err := GetCachedHostedClusterForCluster(ctx, readDesireLister, subscriptionName, resourceGroupName, clusterName)
	if err != nil {
		return uuid.Nil, false, err
	}
	if hostedCluster == nil {
		logger.Info("hosted cluster not found")
		return uuid.Nil, false, nil
	}
	if len(hostedCluster.Spec.ClusterID) == 0 {
		logger.Info("missing cluster UUID")
		return uuid.Nil, false, nil
	}
	clusterUUID, err := uuid.Parse(hostedCluster.Spec.ClusterID)
	if err != nil {
		return uuid.Nil, false, utils.TrackError(fmt.Errorf("failed to parse cluster UUID: %w", err))
	}
	return clusterUUID, true, nil
}
