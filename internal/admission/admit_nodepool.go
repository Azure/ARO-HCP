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

package admission

import (
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

// AdmitNodePool performs non-static checks of nodepool.  Checks that require more information than is contained inside of
// of the nodepool instance itself.
func AdmitNodePool(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// Check only if it is a creating nodepool or a change in the channelGroup
	if (oldNodePool == nil || newNodePool.Properties.Version.ChannelGroup != oldNodePool.Properties.Version.ChannelGroup) &&
		newNodePool.Properties.Version.ChannelGroup != cluster.CustomerProperties.Version.ChannelGroup {
		errs = append(errs, field.Invalid(
			field.NewPath("properties", "version", "channelGroup"),
			newNodePool.Properties.Version.ChannelGroup,
			fmt.Sprintf("must be the same as control plane channel group '%s'", cluster.CustomerProperties.Version.ChannelGroup),
		))
	}

	// Check only if it is a creating nodepool or a change in the Subnet
	if (oldNodePool == nil || newNodePool.Properties.Platform.SubnetID != oldNodePool.Properties.Platform.SubnetID) &&
		newNodePool.Properties.Platform.SubnetID != nil && cluster.CustomerProperties.Platform.SubnetID != nil {
		clusterVNet := cluster.CustomerProperties.Platform.SubnetID.Parent.String()
		nodePoolVNet := newNodePool.Properties.Platform.SubnetID.Parent.String()
		if !strings.EqualFold(nodePoolVNet, clusterVNet) {
			errs = append(errs, field.Invalid(
				field.NewPath("properties", "platform", "subnetId"),
				newNodePool.Properties.Platform.SubnetID,
				fmt.Sprintf("must belong to the same VNet as the parent cluster VNet '%s'", clusterVNet),
			))
		}
	}

	return errs
}

// AdmitNodePoolUpdate performs update-specific validation that requires both the old and new node pool states.
// It includes all validations from AdmitNodePool plus version upgrade constraints.
// The spNodePool and spCluster parameters provide the service provider state for version validation.
func AdmitNodePoolUpdate(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster,
	spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster) field.ErrorList {
	errs := field.ErrorList{}

	// Include all standard node pool admission checks
	errs = append(errs, AdmitNodePool(newNodePool, oldNodePool, cluster)...)

	// Add update-specific version upgrade validation
	errs = append(errs, validateNodePoolVersionUpgrade(newNodePool, oldNodePool, spNodePool, spCluster)...)

	return errs
}

// validateNodePoolVersionUpgrade validates that a node pool version change is a valid upgrade.
// It checks:
//   - No downgrade: new version >= old version
//   - No major version change: new major == old major
//   - No skipping minor versions: new minor <= old minor + 1
//   - Cannot exceed cluster version: new version <= cluster version
func validateNodePoolVersionUpgrade(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster) field.ErrorList {

	// Skip validation if no version is specified or version didn't change
	if len(newNodePool.Properties.Version.ID) == 0 || newNodePool.Properties.Version.ID == oldNodePool.Properties.Version.ID {
		return nil
	}

	newVersion := semver.MustParse(newNodePool.Properties.Version.ID)
	// Skip validation if the newVersion hasn't changed from the desired Version
	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
		newVersion.EQ(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return nil
	}

	nodePoolActiveVersions := spNodePool.Status.NodePoolVersion.ActiveVersions

	// Check if the newVersion is already in the activeVersions
	if isVersionInActiveVersions(newVersion, nodePoolActiveVersions) {
		return nil
	}

	errs := field.ErrorList{}
	fldPath := field.NewPath("properties", "version", "id")
	// Check if the newVersion <= control plane versions
	// TODO: We may relax this constraint in the future
	clusterActiveVersions := spCluster.Status.ControlPlaneVersion.ActiveVersions
	if len(clusterActiveVersions) > 0 {
		lowestControlPlane := clusterActiveVersions[len(clusterActiveVersions)-1].Version
		if newVersion.GT(*lowestControlPlane) {
			errs = append(errs, field.Invalid(
				fldPath,
				newNodePool.Properties.Version.ID,
				fmt.Sprintf("invalid node pool version %s: cannot exceed control plane version %s",
					newVersion.String(), lowestControlPlane.String()),
			))
		}
	}

	if len(nodePoolActiveVersions) > 0 {
		highestActive := nodePoolActiveVersions[0].Version
		lowestActive := nodePoolActiveVersions[len(nodePoolActiveVersions)-1].Version
		// No partial downgrades: Node pool version >= highest active version
		if newVersion.LT(*highestActive) {
			errs = append(errs, field.Invalid(
				fldPath,
				newNodePool.Properties.Version.ID,
				fmt.Sprintf("cannot downgrade from version %s to %s", highestActive.String(), newVersion.String()),
			))
		}

		if newVersion.LE(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
			errs = append(errs, field.Invalid(
				fldPath,
				newNodePool.Properties.Version.ID,
				fmt.Sprintf("cannot downgrade from version %s to %s", spNodePool.Spec.NodePoolVersion.DesiredVersion.String(), newVersion.String()),
			))
		}
		// No major version change
		// TODO: Add support for major version upgrades (e.g., 4.20 → 5.0) when needed
		if newVersion.Major != lowestActive.Major {
			errs = append(errs, field.Invalid(
				fldPath,
				newNodePool.Properties.Version.ID,
				fmt.Sprintf("invalid upgrade path from %s to %s: major version changes are not supported",
					lowestActive.String(), newVersion.String()),
			))
		}
		//Don't skip minor
		// TODO: We will relax this constraint in the future to allow skipping minor versions
		if newVersion.Minor > lowestActive.Minor+1 {
			errs = append(errs, field.Invalid(
				fldPath,
				newNodePool.Properties.Version.ID,
				fmt.Sprintf("invalid upgrade path from %s to %s: skipping minor versions is not allowed",
					lowestActive.String(), newVersion.String()),
			))
		}

	}

	return errs
}

// isVersionInActiveVersions checks if the given version is already in the list of active versions.
func isVersionInActiveVersions(version semver.Version, activeVersions []api.HCPNodePoolActiveVersion) bool {
	return slices.ContainsFunc(activeVersions, func(av api.HCPNodePoolActiveVersion) bool {
		return av.Version != nil && av.Version.EQ(version)
	})
}
