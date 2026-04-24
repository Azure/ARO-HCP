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
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// NodePoolAdmissionContext carries dependencies that node pool mutation needs
// beyond the node pool object itself, currently the parent cluster.
type NodePoolAdmissionContext struct {
	Cluster *api.HCPOpenShiftCluster
}

// MutateNodePool applies admission-time mutations to a node pool (e.g. defaulting
// the subnet from the parent cluster on CREATE). It returns any field errors
// produced by the mutation step.
func MutateNodePool(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, newObj, oldObj *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	//Properties HCPOpenShiftClusterNodePoolProperties `json:"properties"`
	errs = append(errs, mutateNodePoolProperties(ctx, admissionContext, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, validation.ToNodePoolProperties))...)

	return errs
}

func mutateNodePoolProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterNodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateNodePoolPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, validation.ToNodePoolPropertiesPlatform))...)

	return errs
}

func mutateNodePoolPlatform(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	if op.Type == operation.Create {
		if newObj.SubnetID == nil {
			newObj.SubnetID = admissionContext.Cluster.CustomerProperties.Platform.DeepCopy().SubnetID
		}
	}

	return errs
}

// admitNodePoolVersionOnCreate validates node pool version constraints against the cluster and control plane versions.
func admitNodePoolVersionOnCreate(np *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster, spCluster *api.ServiceProviderCluster) field.ErrorList {
	if len(np.Properties.Version.ID) == 0 {
		return nil
	}

	errs := field.ErrorList{}
	fieldPath := field.NewPath("properties", "version", "id")

	// Validate against cluster desired version with n-2 skew
	errs = append(errs, validateNodePoolDesiredMinorSkew(np.Properties.Version.ID, cluster.CustomerProperties.Version.ID, 2, fieldPath)...)

	lowestClusterVersion, highestClusterVersion := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	if lowestClusterVersion == nil || highestClusterVersion == nil {
		return errs
	}

	// Node pool can trail by at most 2 minors from the highest active version (n-2)
	errs = append(errs, validateNodePoolDesiredMinorSkew(np.Properties.Version.ID, highestClusterVersion.String(), 2, fieldPath)...)
	return errs
}

// validateNodePoolDesiredMinorSkew checks that the node pool OpenShift version fits the cluster version it is tied to:
//   - Same major: node pool must not be newer than the cluster line; it may trail by at most the number of minors given as the skew parameter.
//   - Cross-major (nodepool → allowed cluster minor lines) is checked based on the skew parameter against the allowlist.
//   - Any other cross-major combination is rejected (the allowlist may grow with product policy).
func validateNodePoolDesiredMinorSkew(nodePoolVersionID, clusterVersionID string, skew int, fieldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	parsedNodePoolVersion, err := semver.ParseTolerant(nodePoolVersionID)
	if err != nil {
		errs = append(errs, field.Invalid(fieldPath, nodePoolVersionID, fmt.Sprintf("invalid nodepool version format: %v", err)))
	}

	parsedClusterVersion, err := semver.ParseTolerant(clusterVersionID)
	if err != nil {
		errs = append(errs, field.Invalid(fieldPath, clusterVersionID, fmt.Sprintf("invalid cluster version format: %v", err)))
	}

	if len(errs) > 0 {
		return errs
	}

	nodePoolMinorReleaseLine := fmt.Sprintf("%d.%d", parsedNodePoolVersion.Major, parsedNodePoolVersion.Minor)
	clusterMinorReleaseLine := fmt.Sprintf("%d.%d", parsedClusterVersion.Major, parsedClusterVersion.Minor)
	nodePoolMinorReleaseVersion, err := semver.ParseTolerant(nodePoolMinorReleaseLine)
	if err != nil {
		errs = append(errs, field.Invalid(fieldPath, nodePoolMinorReleaseLine, fmt.Sprintf("invalid nodepool minor release line: %v", err)))
	}
	clusterMinorReleaseVersion, err := semver.ParseTolerant(clusterMinorReleaseLine)
	if err != nil {
		errs = append(errs, field.Invalid(fieldPath, clusterMinorReleaseLine, fmt.Sprintf("invalid cluster minor release line: %v", err)))
	}

	if len(errs) > 0 {
		return errs
	}

	// Same minor version is always valid
	if nodePoolMinorReleaseVersion.EQ(clusterMinorReleaseVersion) {
		return nil
	}

	// Same major version: check skew
	if parsedNodePoolVersion.Major == parsedClusterVersion.Major {
		// Node pool cannot be newer than cluster
		if nodePoolMinorReleaseVersion.GT(clusterMinorReleaseVersion) {
			return field.ErrorList{field.Invalid(fieldPath, nodePoolVersionID,
				fmt.Sprintf("node pool minor version %s must not exceed cluster minor version %s", nodePoolMinorReleaseLine, clusterMinorReleaseLine),
			)}
		}

		calculatedMin := uint64(max(int64(0), int64(parsedClusterVersion.Minor)-int64(skew)))

		if int64(parsedNodePoolVersion.Minor) >= int64(calculatedMin) {
			return nil
		}
		return field.ErrorList{field.Invalid(fieldPath, nodePoolVersionID, fmt.Sprintf("node pool minor version '%s' cannot be more than %d minors below '%s'",
			nodePoolMinorReleaseLine, skew, clusterMinorReleaseLine),
		)}
	}

	// Cross-major: check allowlist with skew
	allowedClusterVersions := api.AllowControlPlaneNodePoolMajorVersionSkew[nodePoolMinorReleaseLine]

	// Find cluster version in allowlist
	i := slices.Index(allowedClusterVersions, clusterMinorReleaseLine)

	// Check if cluster version is found and within skew range
	// i == -1: cluster version not in allowlist for this nodepool
	// i >= skew: cluster version found but exceeds allowed skew
	//   e.g., skew=1 means only index 0 allowed (strictest), skew=2 means index 0,1 allowed
	if i == -1 || i >= skew {
		errs = append(errs, field.Invalid(fieldPath, nodePoolVersionID, fmt.Sprintf("node pool minor version %s is not compatible with cluster minor version %s",
			nodePoolMinorReleaseLine, clusterMinorReleaseLine)))
	}

	return errs
}

// admitNodePoolCommon performs admission checks that depend on the parent cluster (channel group, subnet/VNet).
// These checks apply to both create and update operations.
func admitNodePoolCommon(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster,
	op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	// Check only if it is a creating nodepool or a change in the channelGroup
	channelGroupChanged := op.Type == operation.Create || newNodePool.Properties.Version.ChannelGroup != oldNodePool.Properties.Version.ChannelGroup
	if channelGroupChanged && newNodePool.Properties.Version.ChannelGroup != cluster.CustomerProperties.Version.ChannelGroup {
		errs = append(errs, field.Invalid(field.NewPath("properties", "version", "channelGroup"), newNodePool.Properties.Version.ChannelGroup,
			fmt.Sprintf("must be the same as control plane channel group '%s'", cluster.CustomerProperties.Version.ChannelGroup),
		))
	}

	// Check only if it is a creating nodepool or a change in the Subnet.
	// Compare by string value (not pointer identity) so equal-but-distinct
	// *azcorearm.ResourceID values aren't treated as a change.
	if newNodePool.Properties.Platform.SubnetID != nil && cluster.CustomerProperties.Platform.SubnetID != nil {
		var oldSubnetID string
		if oldNodePool != nil && oldNodePool.Properties.Platform.SubnetID != nil {
			oldSubnetID = oldNodePool.Properties.Platform.SubnetID.String()
		}
		newSubnetID := newNodePool.Properties.Platform.SubnetID.String()
		if op.Type == operation.Create || oldNodePool == nil || !strings.EqualFold(newSubnetID, oldSubnetID) {
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
	}

	return errs
}

// AdmitNodePoolCreate runs create-time admission that depends on the parent cluster and service-provider cluster data.
func AdmitNodePoolCreate(np *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster, spCluster *api.ServiceProviderCluster,
	op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	// Channel group matches cluster; node pool subnet is on the cluster VNet.
	errs = append(errs, admitNodePoolCommon(np, nil, cluster, op)...)
	errs = append(errs, admitNodePoolVersionOnCreate(np, cluster, spCluster)...)

	return errs
}

// AdmitNodePoolUpdate performs update-specific validation that requires both the old and new node pool states.
// It includes all validations from admitNodePoolCommon plus version upgrade constraints.
// The spNodePool and spCluster parameters provide the service provider state for version validation.
func AdmitNodePoolUpdate(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster,
	spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster, op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolCommon(newNodePool, oldNodePool, cluster, op)...)

	// Add update-specific version upgrade validation
	errs = append(errs, validateNodePoolVersionUpgrade(newNodePool, oldNodePool, spNodePool, cluster, spCluster, op)...)

	return errs
}

// validateNodePoolVersionUpgrade validates that a node pool version change is a valid upgrade.
// It checks:
//   - No downgrade: new version >= desired version
//   - No major version change: new major == old major (unless FeatureExperimentalReleaseFeatures is registered)
//   - No skipping minor versions: new minor <= old minor + 1
//   - Within n-2 minor version skew of cluster's desired version
func validateNodePoolVersionUpgrade(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, spNodePool *api.ServiceProviderNodePool,
	cluster *api.HCPOpenShiftCluster, spCluster *api.ServiceProviderCluster, op operation.Operation) field.ErrorList {

	// Skip validation if no version is specified or version didn't change
	if len(newNodePool.Properties.Version.ID) == 0 || newNodePool.Properties.Version.ID == oldNodePool.Properties.Version.ID {
		return nil
	}

	errs := field.ErrorList{}
	fieldPath := field.NewPath("properties", "version", "id")

	// Validate against cluster desired version with n-2 skew
	errs = append(errs, validateNodePoolDesiredMinorSkew(newNodePool.Properties.Version.ID, cluster.CustomerProperties.Version.ID, 2, fieldPath)...)

	newVersion, err := semver.Parse(newNodePool.Properties.Version.ID)
	if err != nil {
		errs = append(errs, field.Invalid(fieldPath, newNodePool.Properties.Version.ID, fmt.Sprintf("invalid node pool version format: %s", err.Error())))
		// Return early, it cannot validate an unparseable version
		return errs
	}
	// Skip validation if the newVersion hasn't changed from the desired Version
	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
		newVersion.EQ(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return nil
	}

	lowestCPVersion, _ := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)
	if err := validation.ValidateNodePoolUpgrade(newVersion, spNodePool.Status.NodePoolVersion.ActiveVersions, lowestCPVersion, op.HasOption(api.FeatureExperimentalReleaseFeatures)); err != nil {
		errs = append(errs, field.Invalid(fieldPath, newNodePool.Properties.Version.ID, err.Error()))
	}

	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil && newVersion.LE(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		errs = append(errs, field.Invalid(fieldPath, newNodePool.Properties.Version.ID, fmt.Sprintf("cannot downgrade from version %s to %s", spNodePool.Spec.NodePoolVersion.DesiredVersion.String(), newVersion.String())))
	}
	return errs
}
