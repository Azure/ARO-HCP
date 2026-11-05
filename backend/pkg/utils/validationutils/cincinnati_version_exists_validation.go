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

package validationutils

import (
	"context"
	"errors"
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NodePoolCincinnatiVersionExistsValidation validates that the requested node pool version
// exists in the Cincinnati update graph for the specified channel.
type NodePoolCincinnatiVersionExistsValidation struct {
	readDesireLister      kubeapplierlisters.ReadDesireLister
	cincinnatiClientCache cincinnati.ClientCache
}

func NewNodePoolCincinnatiVersionExistsValidation(
	readDesireLister kubeapplierlisters.ReadDesireLister,
	cincinnatiClientCache cincinnati.ClientCache,
) *NodePoolCincinnatiVersionExistsValidation {
	return &NodePoolCincinnatiVersionExistsValidation{
		readDesireLister:      readDesireLister,
		cincinnatiClientCache: cincinnatiClientCache,
	}
}

var _ NodePoolValidation = (*NodePoolCincinnatiVersionExistsValidation)(nil)

func (v *NodePoolCincinnatiVersionExistsValidation) Name() string {
	return "NodePoolCincinnatiVersionExistsValidation"
}

func (v *NodePoolCincinnatiVersionExistsValidation) Validate(ctx context.Context, cluster *coreapi.Cluster, _ *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	logger := utils.LoggerFromContext(ctx)

	version, err := semver.Parse(nodePool.Properties.Version.ID)
	if err != nil {
		// Invalid semver format - this should be caught by static validation
		// but treat as failed here
		msg := fmt.Sprintf("Invalid version format %q: %s", nodePool.Properties.Version.ID, err.Error())
		return FailedValidation("InvalidVersionFormat", msg, msg)
	}

	// Get Cincinnati URI
	channelGroup := nodePool.Properties.Version.ChannelGroup
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify that version is available.",
			fmt.Sprintf("failed to get Cincinnati URI for channel group %q: %s", channelGroup, err.Error()),
			ControllerReportingPolicyTypeError,
		)
	}

	// Resolve cluster UUID from the cached HostedCluster
	// Use best effort - if we can't find it, use an empty UUID
	clusterUUID, found, err := kubeapplierhelpers.GetCachedHostedClusterUUIDForCluster(
		ctx,
		v.readDesireLister,
		cluster.ID.SubscriptionID,
		cluster.ID.ResourceGroupName,
		cluster.Name,
	)
	if err != nil || !found {
		logger.Info("failed to retrieve cluster UUID from cached HostedCluster, continuing with empty",
			"clusterResourceID", cluster.ID.String(), "found", found, "error", err)
		clusterUUID = uuid.UUID{}
	}

	// Query Cincinnati to check if version exists
	cincinnatiChannel := fmt.Sprintf("%s-%d.%d", channelGroup, version.Major, version.Minor)
	cincinnatiClient := v.cincinnatiClientCache.GetOrCreateClient(clusterUUID)

	_, _, _, err = cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, version)
	if err != nil {
		if cincinnati.IsCincinnatiVersionNotFoundError(err) {
			return FailedValidation("VersionNotFound",
				fmt.Sprintf("Version %q in channel group %q is not available.", version.String(), channelGroup),
				fmt.Sprintf("version %s not available in Cincinnati channel %s: %s", version.String(), cincinnatiChannel, err.Error()))
		}

		var cincinnatiErr *cvocincinnati.Error
		if errors.As(err, &cincinnatiErr) {
			return UnknownValidation(
				"CincinnatiUnavailable",
				"Unable to verify that version is available due to temporary service issue.",
				fmt.Sprintf("cincinnati query failed for version %q in cincinnati channel %q: %s",
					version.String(), cincinnatiChannel, err.Error()),
				ControllerReportingPolicyTypeError,
			)
		}

		return UnknownValidation(
			"InternalError",
			"Unable to verify that version is available.",
			fmt.Sprintf("failed to query Cincinnati for version %q in channel group %q: %s", version.String(), channelGroup, err.Error()),
			ControllerReportingPolicyTypeError,
		)
	}

	// Version exists in Cincinnati - validation passed
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected,
		fmt.Sprintf("Version %s is available.", version.String()),
		fmt.Sprintf("version %s is available in Cincinnati channel %s", version.String(), cincinnatiChannel))
}
