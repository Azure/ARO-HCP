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

package admission

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

// ValidateClusterNodePoolsMinorVersionSkew lists all node pools for the cluster
// in Cosmos (along with their service provider records) and delegates to
// admitClusterNodePoolsMinorVersionSkew. The frontend cluster admission path
// prefetches the same list into ClusterAdmissionContext.ClusterNodePools and
// calls the inner helper directly; this entry point exists for callers that
// don't have an admission context handy (currently the backend upgrade
// controller).
func ValidateClusterNodePoolsMinorVersionSkew(ctx context.Context, resourcesDBClient database.ResourcesDBClient, clusterResourceID *azcorearm.ResourceID, clusterVersion semver.Version) error {
	nodePoolIterator, err := resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).NodePools(clusterResourceID.Name).List(ctx, nil)
	if err != nil {
		return errors.New("cannot validate node pool skew")
	}
	var clusterNodePools []ClusterAdmissionNodePool
	for _, nodePool := range nodePoolIterator.Items(ctx) {
		spNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, resourcesDBClient, nodePool.ID)
		if err != nil {
			return errors.New("cannot validate node pool skew")
		}
		clusterNodePools = append(clusterNodePools, ClusterAdmissionNodePool{
			NodePool:                nodePool,
			ServiceProviderNodePool: spNodePool,
		})
	}
	if err := nodePoolIterator.GetError(); err != nil {
		return errors.New("cannot validate node pool skew")
	}
	return admitClusterNodePoolsMinorVersionSkew(ctx, clusterNodePools, clusterVersion)
}

// admitClusterNodePoolsMinorVersionSkew walks the prefetched node pools for the
// cluster and checks the desired clusterVersion against each pool using the
// same skew rules (n-2 minor, cross-major allowlist) for:
//   - the customer node pool properties.version.id (when non-empty),
//   - the service provider node pool lowest and highest active versions when
//     they exist.
func admitClusterNodePoolsMinorVersionSkew(_ context.Context, clusterNodePools []ClusterAdmissionNodePool, clusterVersion semver.Version) error {
	var errs []error
	for _, entry := range clusterNodePools {
		nodePool := entry.NodePool
		if nodePool == nil {
			continue
		}
		if len(nodePool.Properties.Version.ID) > 0 {
			parsed, err := semver.ParseTolerant(nodePool.Properties.Version.ID)
			if err != nil {
				errs = append(errs, fmt.Errorf("cannot validate skew for node pool %q: invalid version %q: %w", nodePool.Name, nodePool.Properties.Version.ID, err))
			} else if err := clusterVersionSkewVersusNodePool(nodePool.Name, parsed, clusterVersion); err != nil {
				errs = append(errs, err)
			}
		}
		if entry.ServiceProviderNodePool != nil {
			lowest, highest := apihelpers.FindLowestAndHighestNodePoolVersion(entry.ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions)
			if lowest != nil && highest != nil {
				if err := clusterVersionSkewVersusNodePool(nodePool.Name, *lowest, clusterVersion); err != nil {
					errs = append(errs, err)
				}
				if err := clusterVersionSkewVersusNodePool(nodePool.Name, *highest, clusterVersion); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func clusterVersionSkewVersusNodePool(nodePoolName string, nodePoolVer, parsedClusterVersion semver.Version) error {

	nodePoolMinorReleaseLine := fmt.Sprintf("%d.%d", nodePoolVer.Major, nodePoolVer.Minor)
	clusterMinorReleaseLine := fmt.Sprintf("%d.%d", parsedClusterVersion.Major, parsedClusterVersion.Minor)
	nodePoolMinorReleaseVersion := api.Must(semver.ParseTolerant(nodePoolMinorReleaseLine))
	clusterMinorReleaseVersion := api.Must(semver.ParseTolerant(clusterMinorReleaseLine))

	if nodePoolMinorReleaseVersion.EQ(clusterMinorReleaseVersion) {
		return nil
	}

	if nodePoolVer.Major == parsedClusterVersion.Major {
		if int64(nodePoolVer.Minor) >= int64(parsedClusterVersion.Minor)-2 {
			return nil
		}
		return fmt.Errorf(
			"cluster minor %s must not be more than two minor versions ahead of node pool %q (minor %s)",
			clusterMinorReleaseLine, nodePoolName, nodePoolMinorReleaseLine,
		)
	}

	allowedClusterVersions := api.AllowControlPlaneNodePoolMajorVersionSkew[nodePoolMinorReleaseLine]
	if slices.Contains(allowedClusterVersions, clusterMinorReleaseLine) {
		return nil
	}
	return fmt.Errorf(
		"cluster minor %s incompatible with node pool %q minor %s",
		clusterMinorReleaseLine, nodePoolName, nodePoolMinorReleaseLine,
	)
}
