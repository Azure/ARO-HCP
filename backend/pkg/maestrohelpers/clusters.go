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

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/json"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func GetCachedHostedClusterForCluster(ctx context.Context, clusterManagementClusterContentLister listers.ManagementClusterContentLister, subscriptionName, resourceGroupName, clusterName string) (*v1beta1.HostedCluster, error) {
	hostedClusterContent, err := clusterManagementClusterContentLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, string(resourcesapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster management cluster content: %w", err))
	}
	if hostedClusterContent.Status.KubeContent == nil {
		return nil, nil
	}
	if len(hostedClusterContent.Status.KubeContent.Items) != 1 {
		return nil, nil
	}
	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(hostedClusterContent.Status.KubeContent.Items[0].Raw, hostedCluster); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal kubecontent: %w", err))
	}
	return hostedCluster, nil
}

// GetCachedHostedClusterUUIDForCluster resolves the cluster UUID parsed from the cached HostedCluster's
// Spec.ClusterID for the given cluster.
//
// Returns (uuid, true, nil) on success.
//
// Returns (uuid.Nil, false, nil) for transient situations the caller should treat as a silent skip:
// the management cluster content has not yet been observed, the kubecontent does not contain exactly
// one HostedCluster, or the HostedCluster's Spec.ClusterID is empty. The reason is logged via the
// context logger so callers don't need to.
//
// Returns a non-nil error only for hard failures: a non-NotFound lister error, malformed kubecontent,
// or an unparseable UUID.
func GetCachedHostedClusterUUIDForCluster(ctx context.Context, clusterManagementClusterContentLister listers.ManagementClusterContentLister, subscriptionName, resourceGroupName, clusterName string) (uuid.UUID, bool, error) {
	logger := utils.LoggerFromContext(ctx)
	hostedCluster, err := GetCachedHostedClusterForCluster(ctx, clusterManagementClusterContentLister, subscriptionName, resourceGroupName, clusterName)
	if database.IsNotFoundError(err) {
		// will reappear once the informer relists; until then there is no UUID to derive
		return uuid.Nil, false, nil
	}
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
